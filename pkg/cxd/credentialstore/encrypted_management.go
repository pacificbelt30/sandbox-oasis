package credentialstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"

	"golang.org/x/sys/unix"
)

// EncryptionInfo contains public metadata only. Fingerprint identifies the
// protected identity and stays unchanged across ordinary OAuth refreshes.
type EncryptionInfo struct{ Backend, KeyID, Fingerprint, Format string }

func (s *EncryptedStore) Info(ctx context.Context) (EncryptionInfo, error) {
	var info EncryptionInfo
	err := s.base.locked(ctx, s.name, func(dir *os.File) error {
		r, err := readEncryptedRecord(dir, s.name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(dataAAD(r.Header))
		format := "age"
		if r.Header.Schema == 1 {
			format = "legacy-v1"
		}
		info = EncryptionInfo{Backend: r.Header.Backend, KeyID: r.Header.KeyID, Fingerprint: hex.EncodeToString(sum[:]), Format: format}
		return nil
	})
	return info, err
}
func validKeyID(id string) bool {
	value, err := hex.DecodeString(id)
	return err == nil && len(value) == 16
}

// Keyring is an optional host-only secret backend. New age profiles store an
// opaque identity/authentication-key bundle; legacy profiles stored 32 bytes.
// Implementations must never fall back to files or another backend on failure.
type Keyring interface {
	Put(context.Context, string, []byte) error
	Get(context.Context, string) ([]byte, error)
	Delete(context.Context, string) error
}

func newKeyringHeader(ctx context.Context, name string, ring Keyring) (encryptedHeader, []byte, error) {
	if ring == nil {
		return encryptedHeader{}, nil, fmt.Errorf("keyring backend is required")
	}
	key, recipient, err := newAgeSecret()
	if err != nil {
		return encryptedHeader{}, nil, err
	}
	id, err := newProfileVersion()
	if err != nil {
		clear(key)
		return encryptedHeader{}, nil, err
	}
	header := encryptedHeader{Schema: ageProfileSchema, Name: name, KeyID: id, Backend: "secret-service", Recipient: recipient}
	if err = ring.Put(ctx, id, key); err != nil {
		clear(key)
		return encryptedHeader{}, nil, err
	}
	stored, err := ring.Get(ctx, id)
	defer clear(stored)
	if err != nil || !bytes.Equal(stored, key) {
		clear(key)
		return encryptedHeader{}, nil, fmt.Errorf("keyring read-back verification failed")
	}
	// Retain the item on uncertain persistence failure: deleting it could make
	// already-committed ciphertext unrecoverable.
	return header, key, nil
}
func keyringRecordKey(ctx context.Context, r encryptedRecord, ring Keyring) ([]byte, error) {
	if r.Header.Backend != "secret-service" || ring == nil {
		return nil, ErrUnlock
	}
	key, err := ring.Get(ctx, r.Header.KeyID)
	if err != nil {
		return nil, err
	}
	if r.Header.Schema == 1 {
		defer clear(key)
		return legacyKeyringKey(r, key)
	}
	if _, err = openEncrypted(r, key); err != nil {
		clear(key)
		return nil, ErrUnlock
	}
	return key, nil
}
func (s *EncryptedStore) auditCreateWithKeyring(ctx context.Context, name string, ring Keyring, credentials Credentials) error {
	if name != s.name || ring == nil {
		return fmt.Errorf("invalid keyring profile")
	}
	return s.base.locked(ctx, name, func(dir *os.File) error {
		for _, filename := range []string{name + ".vault", name + ".json"} {
			f, err := openProfileFile(dir, filename, unix.O_RDONLY)
			if err == nil {
				_ = f.Close()
				return fmt.Errorf("profile already exists")
			}
			if !errors.Is(err, unix.ENOENT) {
				return err
			}
		}
		value, err := newCredentialConnection(credentials)
		if err != nil {
			return err
		}
		header, key, err := newKeyringHeader(ctx, name, ring)
		if err != nil {
			return err
		}
		defer clear(key)
		record, err := sealEncrypted(header, key, value)
		if err != nil {
			return err
		}
		if err = writeEncrypted(ctx, dir, record); err != nil {
			return err
		}
		s.setKey(key, header)
		return nil
	})
}
func (s *EncryptedStore) UnlockWithKeyring(ctx context.Context, ring Keyring) error {
	return s.base.locked(ctx, s.name, func(dir *os.File) error {
		record, err := readEncryptedRecord(dir, s.name)
		if err != nil {
			return err
		}
		key, err := keyringRecordKey(ctx, record, ring)
		if err != nil {
			return err
		}
		defer clear(key)
		if err = ctx.Err(); err != nil {
			return err
		}
		s.setKey(key, record.Header)
		return nil
	})
}
func newCredentialConnection(value Credentials) (Credentials, error) {
	var err error
	value.Version, err = newProfileVersion()
	if err != nil {
		return Credentials{}, err
	}
	value.ConnectionVersion, err = newProfileVersion()
	if err != nil {
		return Credentials{}, err
	}
	if err = validateFileCredentials(value); err != nil {
		return Credentials{}, err
	}
	return value, nil
}
func sameCredentialContent(a, b Credentials) bool {
	a.Version, b.Version = "", ""
	a.ConnectionVersion, b.ConnectionVersion = "", ""
	return reflect.DeepEqual(a, b)
}
func persistVerifiedAge(ctx context.Context, dir *os.File, header encryptedHeader, key []byte, value Credentials) error {
	record, err := sealEncrypted(header, key, value)
	if err != nil {
		return err
	}
	if _, err = openEncrypted(record, key); err != nil {
		return err
	}
	if err = writeEncrypted(ctx, dir, record); err != nil {
		return err
	}
	saved, err := readEncryptedRecord(dir, header.Name)
	if err != nil {
		return err
	}
	reloaded, err := openEncrypted(saved, key)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(reloaded, value) {
		return ErrConflict
	}
	return nil
}

// EncryptPlaintext explicitly migrates plaintext. An interrupted migration
// leaves both records and fails closed; repeat it with the same passphrase to
// compare credentials before removing plaintext. Old interrupted schema-1
// migrations are upgraded only through this explicit operation.
func (s *EncryptedStore) auditEncryptPlaintext(ctx context.Context, pass []byte) error {
	return s.base.locked(ctx, s.name, func(dir *os.File) error {
		plain, err := readProfile(dir, s.name)
		if err != nil {
			return err
		}
		existing, err := readEncryptedRecord(dir, s.name)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		var header encryptedHeader
		var key []byte
		if err == nil {
			key, err = passphraseRecordKey(existing, pass)
			if err != nil {
				return err
			}
			defer clear(key)
			saved, err := openEncrypted(existing, key)
			if err != nil {
				return err
			}
			if !sameCredentialContent(plain, saved) {
				return ErrConflict
			}
			if existing.Header.Schema == ageProfileSchema {
				header = existing.Header
				plain = saved
			}
		}
		if header.Schema != ageProfileSchema {
			header, key, err = newPassphraseHeader(s.name, pass)
			if err != nil {
				return err
			}
			defer clear(key)
			plain, err = newCredentialConnection(plain)
			if err != nil {
				return err
			}
			if err = persistVerifiedAge(ctx, dir, header, key, plain); err != nil {
				return err
			}
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = unix.Unlinkat(int(dir.Fd()), s.name+".json", 0); err != nil {
			return err
		}
		if err = dir.Sync(); err != nil {
			return err
		}
		s.setKey(key, header)
		return nil
	})
}

// MigrateToAge explicitly replaces a schema-1 encrypted record, rotates its
// connection generation, and retains a private encrypted recovery backup. It
// never writes plaintext. Callers must drain workers and remove old proxies.
func (s *EncryptedStore) auditMigrateToAge(ctx context.Context, pass []byte, ring Keyring) error {
	return s.base.locked(ctx, s.name, func(dir *os.File) error {
		if f, err := openProfileFile(dir, s.name+".json", unix.O_RDONLY); err == nil {
			_ = f.Close()
			return ErrMigrationPending
		} else if !errors.Is(err, unix.ENOENT) {
			return err
		}
		record, err := readEncryptedRecord(dir, s.name)
		if err != nil {
			return err
		}
		var oldKey []byte
		switch record.Header.Backend {
		case "passphrase":
			oldKey, err = passphraseRecordKey(record, pass)
		case "secret-service":
			oldKey, err = keyringRecordKey(ctx, record, ring)
		default:
			return ErrUnlock
		}
		if err != nil {
			return err
		}
		defer clear(oldKey)
		if record.Header.Schema == ageProfileSchema {
			s.setKey(oldKey, record.Header)
			return nil
		}
		value, err := openEncrypted(record, oldKey)
		if err != nil {
			return err
		}
		value, err = newCredentialConnection(value)
		if err != nil {
			return err
		}
		var header encryptedHeader
		var key []byte
		if record.Header.Backend == "passphrase" {
			header, key, err = newPassphraseHeader(s.name, pass)
		} else {
			header, key, err = newKeyringHeader(ctx, s.name, ring)
		}
		if err != nil {
			return err
		}
		defer clear(key)
		// Prepare/verify the new ciphertext before retaining the original backup.
		prepared, err := sealEncrypted(header, key, value)
		if err != nil {
			return err
		}
		if _, err = openEncrypted(prepared, key); err != nil {
			return err
		}
		if err = backupLegacyRecord(ctx, dir, s.name, record); err != nil {
			return err
		}
		if err = persistVerifiedAge(ctx, dir, header, key, value); err != nil {
			return err
		}
		s.setKey(key, header)
		return nil
	})
}
func backupLegacyRecord(ctx context.Context, dir *os.File, name string, record encryptedRecord) error {
	filename := name + ".vault.v1-backup"
	f, err := openProfileFile(dir, filename, unix.O_RDONLY)
	if err == nil {
		data, readErr := io.ReadAll(io.LimitReader(f, (2<<20)+1))
		_ = f.Close()
		var saved encryptedRecord
		if readErr != nil || len(data) > 2<<20 || json.Unmarshal(data, &saved) != nil || !reflect.DeepEqual(saved, record) {
			return fmt.Errorf("existing legacy backup differs; retain and inspect it before retrying migration")
		}
		return nil
	}
	if !errors.Is(err, unix.ENOENT) {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return writeProfileData(ctx, dir, filename, data)
}

// ChangePassphrase rewraps the same age identity/authentication-key bundle.
// Existing proxy processes must be removed by the caller before changing it.
func (s *EncryptedStore) auditChangePassphrase(ctx context.Context, pass []byte) error {
	return s.base.locked(ctx, s.name, func(dir *os.File) error {
		value, err := s.load(dir)
		if err != nil {
			return err
		}
		s.mu.Lock()
		previous := s.header
		s.mu.Unlock()
		if previous.Schema != ageProfileSchema {
			return ErrMigrationRequired
		}
		if previous.Backend != "passphrase" {
			return fmt.Errorf("profile does not use a passphrase")
		}
		key, err := s.UnlockedKey()
		if err != nil {
			return err
		}
		defer clear(key)
		header, err := protectPassphraseKey(s.name, pass, key)
		if err != nil {
			return err
		}
		if err = persistVerifiedAge(ctx, dir, header, key, value); err != nil {
			return err
		}
		s.setKey(key, header)
		return nil
	})
}
