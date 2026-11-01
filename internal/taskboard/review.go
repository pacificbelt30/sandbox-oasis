package taskboard

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/streamfile"
)

type Hunk struct {
	ID                           string `json:"id"`
	Header                       string `json:"header"`
	Body                         string `json:"body"`
	OldStart, OldCount, NewCount int
}
type Change struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Patch     string `json:"patch"`
	Prefix    string `json:"prefix"`
	Hunks     []Hunk `json:"hunks"`
	WholeOnly bool   `json:"whole_only"`
}
type Review struct {
	Version  int      `json:"version"`
	ID       string   `json:"id"`
	Task     string   `json:"task"`
	Attempt  int      `json:"attempt"`
	Source   string   `json:"source"`
	Changes  []Change `json:"changes"`
	Excluded []string `json:"excluded"`
}
type Selection map[string][]string // empty hunk list selects the entire file

type Acceptance struct {
	ID           string    `json:"id"`
	Review       string    `json:"review"`
	TargetBranch string    `json:"target_branch"`
	Target       string    `json:"target"`
	ResultBranch string    `json:"result_branch"`
	Selection    Selection `json:"selection"`
	Commit       string    `json:"commit"`
	Created      time.Time `json:"created"`
}

func digestText(data []byte) string { v := sha256.Sum256(data); return hex.EncodeToString(v[:]) }
func safeArtifact(name string) bool {
	if name == "" || len(name) > 4096 || path.Clean(name) != name || strings.HasPrefix(name, "/") || name == ".." || strings.HasPrefix(name, "../") {
		return false
	}
	for _, c := range name {
		if c < 32 || c == 127 {
			return false
		}
	}
	for _, p := range strings.Split(name, "/") {
		n := strings.ToLower(p)
		switch n {
		case ".git", ".ssh", ".aws", ".azure", ".config", ".codex", ".claude", "auth.json", "credentials.json", ".npmrc", ".netrc", ".pypirc":
			return false
		}
		if strings.HasPrefix(n, ".env") || strings.HasSuffix(n, ".pem") || strings.HasSuffix(n, ".key") || strings.HasSuffix(n, ".p12") || strings.HasSuffix(n, ".pfx") {
			return false
		}
	}
	return true
}

var reviewIDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

var hunkRE = regexp.MustCompile(`^@@ -([0-9]+)(?:,([0-9]+))? \+([0-9]+)(?:,([0-9]+))? @@(.*)$`)

func parseChange(name, status, patch string) (Change, error) {
	c := Change{Path: name, Status: status, Patch: patch, Hunks: []Hunk{}, WholeOnly: status != "M" || strings.Contains(patch, "GIT binary patch") || strings.Contains(patch, "old mode ") || strings.Contains(patch, "new mode ")}
	header := strings.SplitN(patch, "\n@@ ", 2)[0]
	if strings.Contains(header, " 160000") || strings.Contains(header, " 120000") {
		return c, fmt.Errorf("symlink and submodule changes are not supported")
	}
	lines := strings.SplitAfter(patch, "\n")
	var prefix strings.Builder
	for i := 0; i < len(lines); {
		line := strings.TrimSuffix(lines[i], "\n")
		if !strings.HasPrefix(line, "@@ ") {
			prefix.WriteString(lines[i])
			i++
			continue
		}
		m := hunkRE.FindStringSubmatch(line)
		if m == nil {
			return c, fmt.Errorf("invalid diff hunk")
		}
		a, _ := strconv.Atoi(m[1])
		old := 1
		newCount := 1
		if m[2] != "" {
			old, _ = strconv.Atoi(m[2])
		}
		if m[4] != "" {
			newCount, _ = strconv.Atoi(m[4])
		}
		start := i
		i++
		for i < len(lines) && !strings.HasPrefix(lines[i], "@@ ") {
			i++
		}
		body := strings.Join(lines[start+1:i], "")
		c.Hunks = append(c.Hunks, Hunk{ID: digestText([]byte(name + "\x00" + line + body))[:16], Header: line, Body: body, OldStart: a, OldCount: old, NewCount: newCount})
	}
	c.Prefix = prefix.String()
	if len(c.Hunks) == 0 {
		c.WholeOnly = true
	}
	return c, nil
}
func Capture(ctx context.Context, t Task) (Review, error) {
	a, ok := Current(t)
	if !ok || a.GitDir == "" {
		return Review{}, fmt.Errorf("task has no prepared workspace")
	}
	r := Repository{Root: a.Workspace, GitDir: a.GitDir, Common: t.Common}
	out, e := r.run(ctx, nil, nil, "rev-parse", "--verify", "HEAD^{commit}")
	if e != nil {
		return Review{}, e
	}
	head := strings.TrimSpace(string(out))
	if !oidPattern.MatchString(head) {
		return Review{}, fmt.Errorf("invalid workspace HEAD")
	}
	out, e = r.run(ctx, nil, nil, "diff", "--name-status", "-z", "--no-renames", t.Base, "--")
	if e != nil {
		return Review{}, e
	}
	entries := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	statuses := map[string]string{}
	if len(out) > 0 {
		if len(entries)%2 != 0 {
			return Review{}, fmt.Errorf("invalid Git change list")
		}
		for i := 0; i < len(entries); i += 2 {
			if entries[i] != "M" && entries[i] != "A" && entries[i] != "D" && entries[i] != "T" {
				return Review{}, fmt.Errorf("unsupported Git status")
			}
			statuses[entries[i+1]] = entries[i]
		}
	}
	out, e = r.run(ctx, nil, nil, "ls-files", "--others", "--exclude-standard", "-z", "--")
	if e != nil {
		return Review{}, e
	}
	for _, name := range strings.Split(string(out), "\x00") {
		if name != "" {
			statuses[name] = "?"
		}
	}
	if len(statuses) > 512 {
		return Review{}, fmt.Errorf("review exceeds 512 files")
	}
	names := make([]string, 0, len(statuses))
	for name := range statuses {
		names = append(names, name)
	}
	sort.Strings(names)
	review := Review{Version: 1, Task: t.ID, Attempt: a.Number, Source: head, Changes: []Change{}, Excluded: []string{}}
	total := 0
	fs, e := os.OpenRoot(a.Workspace)
	if e != nil {
		return review, e
	}
	defer func() { _ = fs.Close() }()
	for _, name := range names {
		if !safeArtifact(name) {
			review.Excluded = append(review.Excluded, name)
			continue
		}
		if statuses[name] != "D" {
			f, e := fs.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
			if e != nil {
				return review, fmt.Errorf("review requires regular contained files: %s", name)
			}
			info, e := f.Stat()
			_ = f.Close()
			if e != nil || !info.Mode().IsRegular() || info.Size() > MaxPatch {
				return review, fmt.Errorf("review file is unsupported or oversized: %s", name)
			}
		}
		var patch []byte
		if statuses[name] == "?" {
			// git diff --no-index returns 1 when a difference exists.
			filters, e := filterOptions(ctx, r.Root, r.GitDir, r.Common)
			if e != nil {
				return review, e
			}
			var runErr error
			patch, runErr = gitDiffNew(ctx, r, filters, name)
			if runErr != nil {
				return review, runErr
			}
		} else {
			patch, e = r.run(ctx, nil, nil, "diff", "--binary", "--full-index", "--no-renames", "--no-ext-diff", "--no-textconv", t.Base, "--", name)
			if e != nil {
				return review, e
			}
		}
		total += len(patch)
		if total > MaxPatch {
			return review, fmt.Errorf("review exceeds 32 MiB")
		}
		c, e := parseChange(name, statuses[name], string(patch))
		if e != nil {
			return review, e
		}
		if len(patch) > 0 {
			review.Changes = append(review.Changes, c)
		}
	}
	canonical, _ := json.Marshal(review)
	review.ID = digestText(canonical)
	return review, nil
}
func gitDiffNew(ctx context.Context, r Repository, filters []string, name string) ([]byte, error) {
	// Use a throwaway index to stage the new file without modifying the real
	// index, then diff the resulting blob through the normal Git patch format.
	p, cleanup, e := temporaryIndex()
	if e != nil {
		return nil, e
	}
	defer cleanup()
	env := []string{"GIT_INDEX_FILE=" + p}
	if _, e = r.run(ctx, env, nil, "read-tree", "--empty"); e != nil {
		return nil, e
	}
	if _, e = r.run(ctx, env, nil, "add", "--", name); e != nil {
		return nil, e
	}
	return r.run(ctx, env, nil, "diff", "--cached", "--binary", "--full-index", "--no-ext-diff", "--no-textconv", "--", name)
}
func SelectedPatch(r Review, selection Selection) ([]byte, error) {
	if len(selection) == 0 {
		return nil, fmt.Errorf("select at least one change")
	}
	var out strings.Builder
	found := 0
	for _, c := range r.Changes {
		ids, ok := selection[c.Path]
		if !ok {
			continue
		}
		found++
		if len(ids) == 0 {
			out.WriteString(c.Patch)
			continue
		}
		if c.WholeOnly {
			return nil, fmt.Errorf("%s requires whole-file selection", c.Path)
		}
		wanted := map[string]bool{}
		for _, id := range ids {
			if wanted[id] {
				return nil, fmt.Errorf("duplicate hunk selection")
			}
			wanted[id] = true
		}
		out.WriteString(c.Prefix)
		delta := 0
		count := 0
		for _, h := range c.Hunks {
			if !wanted[h.ID] {
				continue
			}
			count++
			start := h.OldStart + delta
			if h.OldCount == 0 {
				start++
			}
			if h.NewCount == 0 {
				start--
			}
			matched := hunkRE.FindStringSubmatch(h.Header)
			if matched == nil {
				return nil, fmt.Errorf("invalid selected hunk")
			}
			suffix := matched[5]
			_, _ = fmt.Fprintf(&out, "@@ -%d,%d +%d,%d @@%s\n%s", h.OldStart, h.OldCount, start, h.NewCount, suffix, h.Body)
			delta += h.NewCount - h.OldCount
		}
		if count != len(wanted) {
			return nil, fmt.Errorf("unknown hunk selection")
		}
	}
	if found != len(selection) {
		return nil, fmt.Errorf("unknown file selection")
	}
	return []byte(out.String()), nil
}
func (s Store) SaveReview(r Review) error {
	if !reviewIDPattern.MatchString(r.ID) {
		return fmt.Errorf("invalid review ID")
	}
	data, e := json.Marshal(r)
	if e != nil {
		return e
	}
	if len(data) > 48<<20 {
		return fmt.Errorf("encoded review exceeds 48 MiB")
	}
	canonical := r
	canonical.ID = ""
	body, _ := json.Marshal(canonical)
	if digestText(body) != r.ID {
		return fmt.Errorf("review checksum mismatch")
	}
	dir := filepath.Join(s.Root, "reviews")
	if e = privateDirectory(dir); e != nil {
		return e
	}
	p := filepath.Join(dir, r.ID+".json")
	if old, e := readReviewFile(p); e == nil {
		if !bytes.Equal(data, old) {
			return fmt.Errorf("review identity collision")
		}
		return nil
	}
	return streamfile.Create(p, func(f *os.File) error { _, e := f.Write(data); return e })
}
func readReviewFile(p string) ([]byte, error) {
	f, e := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	defer func() { _ = f.Close() }()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || st.Size() > 48<<20 {
		return nil, fmt.Errorf("invalid private review file")
	}
	stat, ok := st.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		return nil, fmt.Errorf("untrusted review ownership")
	}
	data, e := io.ReadAll(io.LimitReader(f, (48<<20)+1))
	if len(data) > 48<<20 {
		return nil, fmt.Errorf("review exceeds limit")
	}
	return data, e
}
func (s Store) Review(id string) (Review, error) {
	var r Review
	if !reviewIDPattern.MatchString(id) {
		return r, fmt.Errorf("full review ID required")
	}
	data, e := readReviewFile(filepath.Join(s.Root, "reviews", id+".json"))
	if e != nil {
		return r, e
	}
	if e = json.Unmarshal(data, &r); e != nil {
		return r, e
	}
	expected := r.ID
	r.ID = ""
	canonical, _ := json.Marshal(r)
	r.ID = expected
	if r.Version != 1 || expected != id || digestText(canonical) != id {
		return r, fmt.Errorf("review checksum mismatch")
	}
	return r, nil
}
