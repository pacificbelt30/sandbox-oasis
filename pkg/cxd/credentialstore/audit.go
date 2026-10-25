package credentialstore

import (
	"context"
	"errors"
	"github.com/pacificbelt30/codex-dock/internal/auditlog"
	"path/filepath"
)

func profileAudit(directory, name string) (func(error) error, error) {
	return (auditlog.Journal{Directory: filepath.Join(directory, "audit")}).Begin("auth_change", name)
}
func profileAuditResult(finish func(error) error, result *error) {
	if finish(*result) != nil {
		*result = errors.New("audit result unavailable; credential update outcome unknown; inspect before retry")
	}
}

func (s *FileStore) Create(ctx context.Context, name string, credentials Credentials) (resultErr error) {
	finish, err := profileAudit(s.directory, name)
	if err != nil {
		return err
	}
	defer profileAuditResult(finish, &resultErr)
	return s.auditCreate(ctx, name, credentials)
}

func (s *FileStore) SaveRefresh(ctx context.Context, name, expected, provider string, next Credentials) (value string, resultErr error) {
	finish, err := profileAudit(s.directory, name)
	if err != nil {
		return "", err
	}
	defer profileAuditResult(finish, &resultErr)
	return s.auditSaveRefresh(ctx, name, expected, provider, next)
}

func (s *EncryptedStore) CreateWithPassphrase(ctx context.Context, name string, passphrase []byte, credentials Credentials) (resultErr error) {
	finish, err := profileAudit(s.base.directory, name)
	if err != nil {
		return err
	}
	defer profileAuditResult(finish, &resultErr)
	return s.auditCreateWithPassphrase(ctx, name, passphrase, credentials)
}

func (s *EncryptedStore) SaveRefresh(ctx context.Context, name, expected, provider string, next Credentials) (value string, resultErr error) {
	finish, err := profileAudit(s.base.directory, name)
	if err != nil {
		return "", err
	}
	defer profileAuditResult(finish, &resultErr)
	return s.auditSaveRefresh(ctx, name, expected, provider, next)
}

func (s *EncryptedStore) CreateWithKeyring(ctx context.Context, name string, ring Keyring, credentials Credentials) (resultErr error) {
	finish, err := profileAudit(s.base.directory, name)
	if err != nil {
		return err
	}
	defer profileAuditResult(finish, &resultErr)
	return s.auditCreateWithKeyring(ctx, name, ring, credentials)
}

func (s *EncryptedStore) EncryptPlaintext(ctx context.Context, pass []byte) (resultErr error) {
	finish, err := profileAudit(s.base.directory, s.name)
	if err != nil {
		return err
	}
	defer profileAuditResult(finish, &resultErr)
	return s.auditEncryptPlaintext(ctx, pass)
}

func (s *EncryptedStore) MigrateToAge(ctx context.Context, pass []byte, ring Keyring) (resultErr error) {
	finish, err := profileAudit(s.base.directory, s.name)
	if err != nil {
		return err
	}
	defer profileAuditResult(finish, &resultErr)
	return s.auditMigrateToAge(ctx, pass, ring)
}

func (s *EncryptedStore) ChangePassphrase(ctx context.Context, pass []byte) (resultErr error) {
	finish, err := profileAudit(s.base.directory, s.name)
	if err != nil {
		return err
	}
	defer profileAuditResult(finish, &resultErr)
	return s.auditChangePassphrase(ctx, pass)
}
