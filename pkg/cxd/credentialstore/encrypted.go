package credentialstore

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"filippo.io/age"
	"fmt"
	"io"
	"os"
	"reflect"
	"runtime"
	"sync"

	"golang.org/x/sys/unix"
)

var (
	ErrMigrationRequired = errors.New("legacy encrypted profile requires explicit migration: oaiso auth profile migrate-age NAME")
	ErrLocked            = errors.New("credential profile is encrypted and locked")
	ErrUnlock            = errors.New("could not unlock credential profile")
	ErrMigrationPending  = errors.New("encrypted and plaintext records coexist; complete the explicit encryption migration")
)

// ProfileStore is the common CLI profile boundary. Existing FileStore callers
// and embedding Store implementations retain their original contracts.
type ProfileStore interface {
	Store
	Create(context.Context, string, Credentials) error
	Directory() string
}

type encryptedHeader struct {
	Schema      int    `json:"schema"`
	Name        string `json:"name"`
	KeyID       string `json:"key_id"`
	Backend     string `json:"backend"`
	Memory      uint32 `json:"memory_kib"`
	Time        uint32 `json:"iterations"`
	Parallelism uint8  `json:"parallelism"`
	Salt        []byte `json:"salt"`
	WrapNonce   []byte `json:"wrap_nonce"`
	WrappedKey  []byte `json:"wrapped_key"`
	Recipient   string `json:"recipient,omitempty"`
}

type encryptedRecord struct {
	Header     encryptedHeader `json:"header"`
	Nonce      []byte          `json:"nonce"`
	Ciphertext []byte          `json:"ciphertext"`
	MAC        []byte          `json:"mac,omitempty"`
}

// EncryptedStore stores age ciphertext and a protected private identity with
// an independent authentication key. An unlocked instance is process-local; no plaintext key file or
// additional persistent daemon is created.
type EncryptedStore struct {
	base              *FileStore
	name              string
	mu                sync.Mutex
	key               []byte
	header            encryptedHeader
	identity          *age.X25519Identity
	authenticationKey []byte
}

func OpenProfile(directory, name string) (ProfileStore, error) {
	s, err := NewFileStore(directory)
	if err != nil {
		return nil, err
	}
	if err := ValidateProfileName(name); err != nil {
		return nil, err
	}
	var encrypted bool
	dir, err := s.openDirectory()
	if err != nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()
	err = func() error {
		_, err := readEncryptedRecord(dir, name)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		encrypted = true
		if f, err := openProfileFile(dir, name+".json", unix.O_RDONLY); err == nil {
			_ = f.Close()
			return ErrMigrationPending
		} else if !errors.Is(err, unix.ENOENT) {
			return err
		}
		return nil
	}()
	if err != nil {
		return nil, err
	}
	if encrypted {
		return NewEncryptedStore(directory, name)
	}
	return s, nil
}

func profileVaultPresent(dir *os.File, name string) (bool, error) {
	f, err := openProfileFile(dir, name+".vault", unix.O_RDONLY)
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, f.Close()
}

func NewEncryptedStore(directory, name string) (*EncryptedStore, error) {
	if err := ValidateProfileName(name); err != nil {
		return nil, err
	}
	base, err := NewFileStore(directory)
	if err != nil {
		return nil, err
	}
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("encrypted profiles require a verified Linux filesystem")
	}
	dir, err := base.openDirectory()
	if err != nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()
	var fs unix.Statfs_t
	if err := unix.Fstatfs(int(dir.Fd()), &fs); err != nil {
		return nil, err
	}
	switch int64(fs.Type) {
	case 0xEF53, 0x01021994, 0x9123683E, 0x58465342, 0x794C7630, 0x2fc12fc1:
	default:
		return nil, fmt.Errorf("encrypted profile filesystem is not verified; use private Linux storage")
	}
	return &EncryptedStore{base: base, name: name}, nil
}

func (s *EncryptedStore) Directory() string { return s.base.Directory() }

func readEncryptedRecord(dir *os.File, name string) (encryptedRecord, error) {
	var value encryptedRecord
	f, err := openProfileFile(dir, name+".vault", unix.O_RDONLY)
	if errors.Is(err, unix.ENOENT) {
		return value, ErrNotFound
	}
	if err != nil {
		return value, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, (2<<20)+1))
	if err != nil || len(data) > 2<<20 || json.Unmarshal(data, &value) != nil {
		return value, fmt.Errorf("invalid encrypted profile")
	}
	h := value.Header
	if h.Name != name || !validKeyID(h.KeyID) || !validEncryptedRecord(value) {
		return encryptedRecord{}, fmt.Errorf("unsupported encrypted profile or key-derivation parameters")
	}
	return value, nil
}

func wrapAAD(h encryptedHeader) []byte {
	h.WrapNonce, h.WrappedKey = nil, nil
	data, _ := json.Marshal(h)
	return append([]byte("oaiso/profile/key/v1\x00"), data...)
}

func dataAAD(h encryptedHeader) []byte {
	data, _ := json.Marshal(h)
	return append([]byte("oaiso/profile/data/v1\x00"), data...)
}

func (s *EncryptedStore) setKey(key []byte, header encryptedHeader) {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.key)
	clear(s.authenticationKey)
	s.key = nil
	s.identity = nil
	s.authenticationKey = nil
	s.header = header
	if header.Schema == ageProfileSchema {
		secret, identity, err := parseAgeSecret(key)
		if err != nil {
			return
		}
		s.identity = identity
		s.authenticationKey = secret.AuthenticationKey
	}
	s.key = append([]byte{}, key...)
}

// Close drops the store's key reference and clears its owned byte slice. Go
// cannot guarantee that every temporary memory copy has been zeroed.
func (s *EncryptedStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.key)
	clear(s.authenticationKey)
	s.key = nil
	s.authenticationKey = nil
	s.identity = nil
	return nil
}

// UnlockedKey returns an owned opaque identity-key bundle solely for host-to-proxy
// IPC. Never put its result in argv, environment, metadata, files or logs.
func (s *EncryptedStore) UnlockedKey() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.key) == 0 || len(s.key) > UnlockKeyMaxSize {
		return nil, ErrLocked
	}
	return append([]byte{}, s.key...), nil
}

func (s *EncryptedStore) Unlock(ctx context.Context, passphrase []byte) error {
	return s.base.locked(ctx, s.name, func(dir *os.File) error {
		record, err := readEncryptedRecord(dir, s.name)
		if err != nil {
			return err
		}
		key, err := passphraseRecordKey(record, passphrase)
		if err != nil {
			return err
		}
		defer clear(key)
		if err := ctx.Err(); err != nil {
			return err
		}
		s.setKey(key, record.Header)
		return nil
	})
}

func (s *EncryptedStore) UnlockWithKey(ctx context.Context, key []byte) error {
	if len(key) == 0 || len(key) > UnlockKeyMaxSize {
		return ErrUnlock
	}
	return s.base.locked(ctx, s.name, func(dir *os.File) error {
		record, err := readEncryptedRecord(dir, s.name)
		if err != nil {
			return err
		}
		if _, err := openEncrypted(record, key); err != nil {
			return ErrUnlock
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		s.setKey(key, record.Header)
		return nil
	})
}

func writeEncrypted(ctx context.Context, dir *os.File, record encryptedRecord) error {
	data, err := json.Marshal(record)
	if err != nil || len(data) > 2<<20 {
		return fmt.Errorf("encrypted profile exceeds supported size")
	}
	return writeProfileData(ctx, dir, record.Header.Name+".vault", data)
}

// CreateWithPassphrase creates only a new encrypted connection; it does not
// replace plaintext or encrypted profiles and never writes plaintext to disk.
func (s *EncryptedStore) auditCreateWithPassphrase(ctx context.Context, name string, passphrase []byte, credentials Credentials) error {
	if name != s.name || len(passphrase) < 12 || len(passphrase) > 4096 {
		return fmt.Errorf("choose a passphrase of 12..4096 bytes for this profile")
	}
	return s.base.locked(ctx, name, func(dir *os.File) error {
		for _, file := range []string{name + ".vault", name + ".json"} {
			if f, err := openProfileFile(dir, file, unix.O_RDONLY); err == nil {
				_ = f.Close()
				return fmt.Errorf("profile already exists; choose a new profile name")
			} else if !errors.Is(err, unix.ENOENT) {
				return err
			}
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
		if err := validateFileCredentials(credentials); err != nil {
			return err
		}
		header, key, err := newPassphraseHeader(name, passphrase)
		if err != nil {
			return err
		}
		defer clear(key)
		record, err := sealEncrypted(header, key, credentials)
		if err != nil {
			return err
		}
		if err := writeEncrypted(ctx, dir, record); err != nil {
			return err
		}
		s.setKey(key, header)
		return nil
	})
}

func (s *EncryptedStore) Create(context.Context, string, Credentials) error {
	return fmt.Errorf("use encrypted profile creation with an explicit unlocking method")
}

func (s *EncryptedStore) load(dir *os.File) (Credentials, error) {
	if f, err := openProfileFile(dir, s.name+".json", unix.O_RDONLY); err == nil {
		_ = f.Close()
		return Credentials{}, ErrMigrationPending
	} else if !errors.Is(err, unix.ENOENT) {
		return Credentials{}, err
	}
	record, err := readEncryptedRecord(dir, s.name)
	if err != nil {
		return Credentials{}, err
	}
	s.mu.Lock()
	header := s.header
	identity := s.identity
	auth := append([]byte{}, s.authenticationKey...)
	var key []byte
	if header.Schema == 1 {
		key = append([]byte{}, s.key...)
	}
	s.mu.Unlock()
	defer clear(auth)
	defer clear(key)
	if sha256.Sum256(dataAAD(header)) != sha256.Sum256(dataAAD(record.Header)) {
		return Credentials{}, ErrLocked
	}
	if header.Schema == ageProfileSchema {
		if identity == nil || len(auth) != 32 {
			return Credentials{}, ErrLocked
		}
		return openAgeEncrypted(record, identity, auth)
	}
	if len(key) == 0 {
		return Credentials{}, ErrLocked
	}
	return legacyOpenEncrypted(record, key)
}

func (s *EncryptedStore) Load(ctx context.Context, name string) (Credentials, error) {
	if name != s.name {
		return Credentials{}, ErrNotFound
	}
	var value Credentials
	err := s.base.locked(ctx, name, func(dir *os.File) (err error) { value, err = s.load(dir); return err })
	return value, err
}

func (s *EncryptedStore) auditSaveRefresh(ctx context.Context, name, expected, provider string, next Credentials) (string, error) {
	if name != s.name {
		return "", ErrNotFound
	}
	var version string
	err := s.base.locked(ctx, name, func(dir *os.File) error {
		old, err := s.load(dir)
		if err != nil {
			return err
		}
		s.mu.Lock()
		schema := s.header.Schema
		s.mu.Unlock()
		if schema != ageProfileSchema {
			return ErrMigrationRequired
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
		s.mu.Lock()
		key := append([]byte{}, s.key...)
		header := s.header
		s.mu.Unlock()
		defer clear(key)
		record, err := sealEncrypted(header, key, next)
		if err != nil {
			return err
		}
		return writeEncrypted(ctx, dir, record)
	})
	if err != nil {
		return "", err
	}
	return version, nil
}
