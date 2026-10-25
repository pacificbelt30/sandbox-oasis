package credentialstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// FileStore implements Store with owner-only plaintext profiles. It deliberately
// has the same refresh contract as a future encrypted store: atomic CAS, distinct
// credential/connection versions, and no fallback to host CLI credentials.
type FileStore struct{ directory string }

var profileName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

func ValidateProfileName(name string) error {
	if !profileName.MatchString(name) {
		return fmt.Errorf("profile name must contain 1-63 lowercase letters, digits, underscores or hyphens")
	}
	return nil
}

func NewFileStore(directory string) (*FileStore, error) {
	directory, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	s := &FileStore{directory: directory}
	f, err := s.openDirectory()
	if err != nil {
		return nil, err
	}
	return s, f.Close()
}

func (s *FileStore) Directory() string { return s.directory }

func privateFile(f *os.File, directory bool) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	want := os.FileMode(0600)
	if directory {
		want = 0700
	}
	if !ok || owner.Uid != uint32(os.Geteuid()) || info.Mode().Perm() != want ||
		(directory && !info.IsDir()) || (!directory && (!info.Mode().IsRegular() || owner.Nlink != 1)) {
		return fmt.Errorf("profile storage must be owned by this user, with directory 0700 and regular files 0600")
	}
	return nil
}

func (s *FileStore) openDirectory() (*os.File, error) {
	fd, err := unix.Open(s.directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), s.directory)
	if err := privateFile(f, true); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func openProfileFile(dir *os.File, name string, flags int) (*os.File, error) {
	fd, err := unix.Openat(int(dir.Fd()), name, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	if err := privateFile(f, false); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func (s *FileStore) locked(ctx context.Context, name string, fn func(*os.File) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if name != "" {
		if err := ValidateProfileName(name); err != nil {
			return err
		}
	}
	dir, err := s.openDirectory()
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	lock, err := openProfileFile(dir, ".lock", unix.O_RDWR|unix.O_CREAT)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }() // Closing releases flock, including on failure.
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn(dir)
}

type fileProfile struct {
	Schema      int         `json:"schema"`
	Name        string      `json:"name"`
	Credentials Credentials `json:"credentials"`
}

func readProfile(dir *os.File, name string) (Credentials, error) {
	f, err := openProfileFile(dir, name+".json", unix.O_RDONLY)
	if errors.Is(err, unix.ENOENT) {
		return Credentials{}, ErrNotFound
	}
	if err != nil {
		return Credentials{}, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return Credentials{}, err
	}
	var record fileProfile
	if len(data) > 1<<20 || json.Unmarshal(data, &record) != nil || record.Schema != 1 || record.Name != name {
		return Credentials{}, fmt.Errorf("invalid profile file")
	}
	if err := validateFileCredentials(record.Credentials); err != nil {
		return Credentials{}, err
	}
	return record.Credentials, nil
}

func newProfileVersion() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func writeProfile(ctx context.Context, dir *os.File, name string, credentials Credentials) error {
	if err := validateFileCredentials(credentials); err != nil {
		return err
	}
	data, err := json.Marshal(fileProfile{Schema: 1, Name: name, Credentials: credentials})
	if err != nil || len(data) > 1<<20 {
		return fmt.Errorf("profile exceeds supported size")
	}
	return writeProfileData(ctx, dir, name+".json", data)
}

func writeProfileData(ctx context.Context, dir *os.File, filename string, data []byte) error {
	id, err := newProfileVersion()
	if err != nil {
		return err
	}
	temporary := ".profile-" + id
	f, err := openProfileFile(dir, temporary, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close(); _ = unix.Unlinkat(int(dir.Fd()), temporary, 0) }()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := unix.Renameat(int(dir.Fd()), temporary, int(dir.Fd()), filename); err != nil {
		return err
	}
	return dir.Sync()
}

// Create never replaces an existing profile. Versions are newly generated,
// independent of imported OAuth data, and cannot be supplied by an importer.
func (s *FileStore) auditCreate(ctx context.Context, name string, credentials Credentials) error {
	if err := ValidateProfileName(name); err != nil {
		return err
	}
	return s.locked(ctx, name, func(dir *os.File) error {
		if present, err := profileVaultPresent(dir, name); err != nil {
			return err
		} else if present {
			return ErrLocked
		}
		if _, err := readProfile(dir, name); !errors.Is(err, ErrNotFound) {
			if err == nil {
				return fmt.Errorf("profile already exists; choose a new profile name")
			}
			return err
		}
		var err error
		credentials.Version, err = newProfileVersion()
		if err != nil {
			return err
		}
		credentials.ConnectionVersion, err = newProfileVersion()
		if err != nil {
			return err
		}
		return writeProfile(ctx, dir, name, credentials)
	})
}

func (s *FileStore) Load(ctx context.Context, name string) (Credentials, error) {
	if err := ValidateProfileName(name); err != nil {
		return Credentials{}, err
	}
	var credentials Credentials
	err := s.locked(ctx, name, func(dir *os.File) (err error) {
		if present, err := profileVaultPresent(dir, name); err != nil {
			return err
		} else if present {
			return ErrLocked
		}
		credentials, err = readProfile(dir, name)
		return err
	})
	return credentials, err
}

func (s *FileStore) auditSaveRefresh(ctx context.Context, name, expected, provider string, next Credentials) (string, error) {
	if err := ValidateProfileName(name); err != nil {
		return "", err
	}
	var version string
	err := s.locked(ctx, name, func(dir *os.File) error {
		if present, err := profileVaultPresent(dir, name); err != nil {
			return err
		} else if present {
			return ErrLocked
		}
		old, err := readProfile(dir, name)
		if err != nil {
			return err
		}
		if old.Version != expected || old.ConnectionVersion != next.ConnectionVersion {
			return ErrConflict
		}
		switch provider {
		case "openai":
			if old.OpenAIOAuth == nil || next.OpenAIOAuth == nil || old.OpenAIAPIKey != next.OpenAIAPIKey || old.AnthropicAPIKey != next.AnthropicAPIKey || !reflect.DeepEqual(old.AnthropicOAuth, next.AnthropicOAuth) {
				return fmt.Errorf("refresh cannot change another provider or authentication mode")
			}
		case "anthropic":
			if old.AnthropicOAuth == nil || next.AnthropicOAuth == nil || old.AnthropicAPIKey != next.AnthropicAPIKey || old.OpenAIAPIKey != next.OpenAIAPIKey || !reflect.DeepEqual(old.OpenAIOAuth, next.OpenAIOAuth) {
				return fmt.Errorf("refresh cannot change another provider or authentication mode")
			}
		default:
			return fmt.Errorf("unsupported refresh provider")
		}
		version, err = newProfileVersion()
		if err != nil {
			return err
		}
		next.Version = version
		return writeProfile(ctx, dir, name, next)
	})
	if err != nil {
		return "", err
	}
	return version, nil
}

func (s *FileStore) Names(ctx context.Context) ([]string, error) {
	var names []string
	err := s.locked(ctx, "", func(dir *os.File) error {
		files, err := dir.Readdirnames(-1)
		if err != nil {
			return err
		}
		for _, file := range files {
			name, ok := strings.CutSuffix(file, ".json")
			if ok && ValidateProfileName(name) == nil {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		return nil
	})
	return names, err
}

func validateFileCredentials(v Credentials) error {
	valid := func(value string) bool { return value != "" && !strings.ContainsAny(value, "\x00\r\n") }
	if !valid(v.Version) || !valid(v.ConnectionVersion) || v.OpenAIAPIKey != "" && v.OpenAIOAuth != nil || v.AnthropicAPIKey != "" && v.AnthropicOAuth != nil ||
		v.OpenAIAPIKey == "" && v.OpenAIOAuth == nil && v.AnthropicAPIKey == "" && v.AnthropicOAuth == nil {
		return fmt.Errorf("profile requires a version and exactly one authentication mode per enabled provider")
	}
	if v.OpenAIAPIKey != "" && !valid(v.OpenAIAPIKey) || v.AnthropicAPIKey != "" && !valid(v.AnthropicAPIKey) {
		return fmt.Errorf("invalid API key")
	}
	if v.OpenAIOAuth != nil && (!valid(v.OpenAIOAuth.AccessToken) || !valid(v.OpenAIOAuth.RefreshToken) || strings.ContainsAny(v.OpenAIOAuth.AccountID, "\x00\r\n")) {
		return fmt.Errorf("incomplete OpenAI OAuth credentials")
	}
	if v.AnthropicOAuth != nil && (!valid(v.AnthropicOAuth.AccessToken) || strings.ContainsAny(v.AnthropicOAuth.RefreshToken, "\x00\r\n")) {
		return fmt.Errorf("incomplete Anthropic OAuth credentials")
	}
	return nil
}
