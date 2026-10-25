package authproxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pacificbelt30/codex-dock/pkg/cxd/credentialstore"
)

type oauthState struct {
	SourceHash  string          `json:"source_hash"`
	Credentials json.RawMessage `json:"credentials"`
}

// The source hash prevents an older refresh result from replacing a new login.
func oauthSourceHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (p *Proxy) restoreOAuthState(provider, sourceToken string, target any) error {
	if p.cfg.OAuthStateDir == "" {
		return nil
	}
	if err := secureStateDir(p.cfg.OAuthStateDir); err != nil {
		return err
	}
	path := filepath.Join(p.cfg.OAuthStateDir, provider+".json")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("insecure OAuth state file %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var state oauthState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("invalid OAuth state: %w", err)
	}
	if state.SourceHash != oauthSourceHash(sourceToken) {
		return nil
	}
	return json.Unmarshal(state.Credentials, target)
}

func secureStateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("insecure OAuth state directory %s", path)
	}
	return nil
}

// persistOAuthState is called before publishing a refreshed credential in memory.
// Callers hold refreshMu. An atomic rename ensures restart sees either generation.
func (p *Proxy) persistOAuthState(provider string, credentials any) (resultErr error) {
	finish, auditErr := p.auditBegin("auth_change", p.credentialScope()+"/"+provider)
	if auditErr != nil {
		p.revokeCaseTokens()
		return auditErr
	}
	defer func() {
		if finish(resultErr) != nil {
			p.revokeCaseTokens()
			resultErr = fmt.Errorf("OAuth audit result unavailable; reconcile credentials before reconnecting")
		}
	}()
	if store := p.cfg.CredentialStore; store != nil {
		next := p.storeCreds
		switch provider {
		case "openai":
			v, ok := credentials.(*OAuthCredentials)
			if !ok || v == nil {
				return fmt.Errorf("invalid OpenAI OAuth refresh")
			}
			next.OpenAIOAuth = &credentialstore.OpenAIOAuth{IDToken: v.IDToken, AccessToken: v.AccessToken, RefreshToken: v.RefreshToken, AccountID: v.AccountID, LastRefresh: v.LastRefresh, ExpiresAt: v.ExpiresAt, TokenType: v.TokenType}
		case "anthropic":
			v, ok := credentials.(*AnthropicOAuthCredentials)
			if !ok || v == nil {
				return fmt.Errorf("invalid Anthropic OAuth refresh")
			}
			next.AnthropicOAuth = &credentialstore.AnthropicOAuth{AccessToken: v.AccessToken, RefreshToken: v.RefreshToken, ExpiresAt: v.ExpiresAt, SubscriptionType: v.SubscriptionType, Scopes: append([]string(nil), v.Scopes...)}
		default:
			return fmt.Errorf("unknown OAuth provider")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		version, err := store.SaveRefresh(ctx, p.credentialScope(), p.storeVersion, provider, next)
		if err != nil {
			// A rotated refresh token may already have invalidated the saved
			// generation even when storage is merely unavailable. Do not keep
			// serving the case after a generation failed to become durable.
			p.revokeCaseTokens()
			return err
		}
		if version == "" {
			p.revokeCaseTokens()
			return fmt.Errorf("external credential store returned empty version")
		}
		next.Version = version
		p.storeCreds, p.storeVersion = next, version
		return nil
	}
	if p.cfg.OAuthStateDir == "" {
		return nil
	}
	if err := secureStateDir(p.cfg.OAuthStateDir); err != nil {
		return err
	}
	encoded, err := json.Marshal(credentials)
	if err != nil {
		return err
	}
	data, err := json.Marshal(oauthState{SourceHash: p.oauthSource[provider], Credentials: encoded})
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(p.cfg.OAuthStateDir, ".oauth-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if err := f.Chmod(0600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), filepath.Join(p.cfg.OAuthStateDir, provider+".json")); err != nil {
		return err
	}
	d, err := os.Open(p.cfg.OAuthStateDir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}
