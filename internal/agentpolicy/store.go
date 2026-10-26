package agentpolicy

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/pacificbelt30/codex-dock/internal/privatefile"
	"github.com/pelletier/go-toml/v2"
)

type Store struct{ Path string }

type Snapshot struct {
	File     File
	Revision string
	Exists   bool
}

func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locating user configuration: %w", err)
	}
	return filepath.Join(dir, "codex-dock", "agent-policies", "config.toml"), nil
}

func NewStore(path string) (*Store, error) {
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return nil, err
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving agent policy path: %w", err)
	}
	abs = filepath.Clean(abs)
	return &Store{Path: abs}, nil
}

func Empty() File { return File{Version: CurrentVersion, Profiles: map[string]Profile{}} }

func (s *Store) Load() (Snapshot, error) {
	data, err := privatefile.Read(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return Snapshot{File: Empty(), Revision: privatefile.Revision(nil), Exists: false}, nil
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("reading agent policies: %w", err)
	}
	file, err := Decode(data)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{File: file, Revision: privatefile.Revision(data), Exists: true}, nil
}

func Decode(data []byte) (File, error) {
	file := Empty()
	if len(data) == 0 {
		return file, nil
	}
	decoder := toml.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return File{}, fmt.Errorf("decoding agent policies: %w", err)
	}
	if file.Profiles == nil {
		file.Profiles = map[string]Profile{}
	}
	if file.Version == 0 {
		file.Version = CurrentVersion
	}
	if err := ValidateFile(file); err != nil {
		return File{}, err
	}
	return file, nil
}

func Encode(file File) ([]byte, error) {
	if file.Version == 0 {
		file.Version = CurrentVersion
	}
	if file.Profiles == nil {
		file.Profiles = map[string]Profile{}
	}
	if err := ValidateFile(file); err != nil {
		return nil, err
	}
	data, err := toml.Marshal(file)
	if err != nil {
		return nil, fmt.Errorf("encoding agent policies: %w", err)
	}
	return data, nil
}

func (s *Store) Save(expected string, file File) error {
	data, err := Encode(file)
	if err != nil {
		return err
	}
	return privatefile.Update(s.Path, expected, func([]byte) ([]byte, error) { return data, nil })
}

func (s *Store) Update(transform func(*File) error) error {
	snapshot, err := s.Load()
	if err != nil {
		return err
	}
	file := cloneFile(snapshot.File)
	if err := transform(&file); err != nil {
		return err
	}
	return s.Save(snapshot.Revision, file)
}

func (s *Store) SetDefault(name string) error {
	name = cleanName(name)
	return s.Update(func(file *File) error {
		if _, ok := mergedProfiles(*file)[name]; !ok {
			return fmt.Errorf("agent policy %q does not exist", name)
		}
		file.Default = name
		return nil
	})
}

func (s *Store) Put(name string, profile Profile) error {
	name = cleanName(name)
	if _, builtin := BuiltIns()[name]; builtin {
		return fmt.Errorf("built-in agent policy %q cannot be replaced", name)
	}
	if err := ValidateProfile(name, profile); err != nil {
		return err
	}
	return s.Update(func(file *File) error {
		if file.Profiles == nil {
			file.Profiles = map[string]Profile{}
		}
		file.Profiles[name] = profile
		return nil
	})
}

func (s *Store) Delete(name string) error {
	name = cleanName(name)
	if _, builtin := BuiltIns()[name]; builtin {
		return fmt.Errorf("built-in agent policy %q cannot be deleted", name)
	}
	return s.Update(func(file *File) error {
		if _, ok := file.Profiles[name]; !ok {
			return fmt.Errorf("agent policy %q does not exist", name)
		}
		for child, profile := range file.Profiles {
			if cleanName(profile.Extends) == name {
				return fmt.Errorf("agent policy %q is extended by %q", name, child)
			}
		}
		if file.Default == name {
			return fmt.Errorf("agent policy %q is the default; select another default first", name)
		}
		delete(file.Profiles, name)
		return nil
	})
}

func (s *Store) Initialize(force bool) error {
	snapshot, err := s.Load()
	if err != nil {
		return err
	}
	if snapshot.Exists && !force {
		return fmt.Errorf("agent policy file already exists: %s", s.Path)
	}
	file := Empty()
	file.Default = "interactive"
	file.Profiles["team"] = Profile{
		Description: "Example host-owned policy; adjust or remove it.",
		Extends:     "approve-for-me",
		Codex:       CodexSettings{ReasoningEffort: String("high")},
		Claude:      ClaudeSettings{Effort: String("high")},
	}
	return s.Save(snapshot.Revision, file)
}

func (s *Store) ExportNames() ([]string, error) {
	snapshot, err := s.Load()
	if err != nil {
		return nil, err
	}
	profiles := mergedProfiles(snapshot.File)
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}
