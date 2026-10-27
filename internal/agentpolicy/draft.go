package agentpolicy

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"

	"github.com/pacificbelt30/codex-dock/internal/privatefile"
)

// Draft stages a whole editing session against the revision that was opened.
// Save never reloads automatically: doing so would overwrite an external edit.
type Draft struct {
	store *Store
	base  Snapshot
	file  File
}

func OpenDraft(store *Store) (*Draft, error) {
	snapshot, err := store.Load()
	if err != nil {
		return nil, err
	}
	return &Draft{store: store, base: snapshot, file: cloneFile(snapshot.File)}, nil
}

func (d *Draft) File() File { return cloneFile(d.file) }

func (d *Draft) Dirty() bool {
	before, _ := Encode(d.base.File)
	after, _ := Encode(d.file)
	return !bytes.Equal(before, after)
}

func (d *Draft) Clone(source, name string) error {
	file, err := Clone(d.file, source, name)
	if err == nil {
		d.file = file
	}
	return err
}

func (d *Draft) SetDefault(name string) error {
	effective, err := Resolve(d.file, name)
	if err != nil {
		return err
	}
	d.file.Default = effective.Name
	return nil
}

func (d *Draft) Change(name, field, value string, inherit bool) error {
	name = cleanName(name)
	if _, builtin := BuiltIns()[name]; builtin {
		return fmt.Errorf("clone built-in policy %q before editing it", name)
	}
	profile, ok := d.file.Profiles[name]
	if !ok {
		return fmt.Errorf("agent policy %q does not exist", name)
	}
	var err error
	if inherit {
		profile, err = UnsetField(profile, field)
	} else {
		profile, err = SetField(profile, field, value)
	}
	if err != nil {
		return err
	}
	file := cloneFile(d.file)
	file.Profiles[name] = profile
	if err := ValidateFile(file); err != nil {
		return err
	}
	d.file = file
	return nil
}

// Review includes descendants affected by an inherited setting, not only the
// selected policy. Raw profile changes are included even if values resolve alike.
func (d *Draft) Review() string {
	var out strings.Builder
	before, _ := Resolve(d.base.File, "")
	after, _ := Resolve(d.file, "")
	fmt.Fprintf(&out, "Host default: %s -> %s\n", before.Name, after.Name)
	out.WriteString("Applies to new workers. Explicit flags, project settings, and global run.agent_policy can override these defaults.\n")
	out.WriteString("Running workers are not changed.\n\n")
	for _, name := range profileNames(d.file.Profiles) {
		old, existed := d.base.File.Profiles[name]
		current := d.file.Profiles[name]
		if !existed || !reflect.DeepEqual(old, current) {
			fmt.Fprintf(&out, "Edited profile: %s (parent: %s -> %s)\n", name, old.Extends, current.Extends)
		}
	}
	all, _ := ResolveAll(d.file)
	for _, name := range profileNames(mergedProfiles(d.file)) {
		current := all[name]
		old, err := Resolve(d.base.File, name)
		differences := Diff(old, current)
		if err == nil && len(differences) == 0 && name != after.Name {
			continue
		}
		fmt.Fprintf(&out, "\nPolicy: %s\n", name)
		for _, difference := range differences {
			fmt.Fprintf(&out, "  %s: %q [%s] -> %q [%s]\n", difference.Path, difference.Left, difference.LeftSource, difference.Right, difference.RightSource)
		}
		for _, finding := range Audit(current).Findings {
			fmt.Fprintf(&out, "  [%s] %s: %s\n", finding.Severity, finding.Title, finding.Detail)
		}
	}
	return out.String()
}

func (d *Draft) Save() error {
	if err := d.store.Save(d.base.Revision, d.file); err != nil {
		return err
	}
	data, err := Encode(d.file)
	if err != nil {
		return err
	}
	// Keep the exact revision written, rather than accepting a later writer's
	// revision from Load while retaining this draft's older values.
	d.base = Snapshot{File: cloneFile(d.file), Revision: privatefile.Revision(data), Exists: true}
	return nil
}
