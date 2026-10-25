package authproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pacificbelt30/codex-dock/pkg/cxd/credentialstore"
	"github.com/pacificbelt30/codex-dock/pkg/cxd/identity"
)

// RemoteProxy talks to an externally running auth proxy service.
type RemoteProxy struct {
	adminURL           string
	containerURL       string
	adminSecret        string
	oauthMode          bool
	openaiMode         bool
	anthropicMode      bool
	anthropicOAuthMode bool
	identityMode       bool
	caseProxy          bool
	profileName        string
	profileConnection  string
	credentialBroker   bool
	modelAuthorization bool
	modelPolicyActive  bool
	tokenModelPolicy   bool
	tokenUsagePolicy   bool
	httpClient         *http.Client
	adminSecretHeader  string
}

func NewRemoteProxy(adminURL, containerURL, adminSecret string) (*RemoteProxy, error) {
	return NewRemoteProxyContext(context.Background(), adminURL, containerURL, adminSecret)
}

// NewRemoteProxyContext lets embedding callers cancel the initial mode probe.
func NewRemoteProxyContext(ctx context.Context, adminURL, containerURL, adminSecret string) (*RemoteProxy, error) {
	r := &RemoteProxy{
		adminURL:          adminURL,
		containerURL:      containerURL,
		adminSecret:       adminSecret,
		httpClient:        &http.Client{Timeout: 10 * time.Second},
		adminSecretHeader: "X-Proxy-Admin-Secret",
	}
	mode, err := r.fetchMode(ctx)
	if err != nil {
		return nil, err
	}
	if !mode["worker_token_auth"] {
		return nil, fmt.Errorf("auth proxy does not enforce worker API tokens; rebuild the proxy image and recreate the proxy before running workers")
	}
	r.oauthMode = mode["oauth_mode"]
	r.openaiMode = mode["openai_available"]
	r.anthropicMode = mode["anthropic_available"]
	r.anthropicOAuthMode = mode["anthropic_oauth_mode"]
	r.identityMode = mode["token_identity"]
	r.caseProxy = mode["case_proxy"]
	r.credentialBroker = mode["credential_broker"]
	r.modelAuthorization = mode["model_authorization"]
	r.modelPolicyActive = mode["model_policy_active"]
	r.tokenModelPolicy = mode["token_model_policy"]
	r.tokenUsagePolicy = mode["token_usage_policy"]
	return r, nil
}

func (r *RemoteProxy) ContainerEndpoint() string        { return r.containerURL }
func (r *RemoteProxy) IsOpenAIMode() bool               { return r.openaiMode }
func (r *RemoteProxy) IsOAuthMode() bool                { return r.oauthMode }
func (r *RemoteProxy) IsAnthropicMode() bool            { return r.anthropicMode }
func (r *RemoteProxy) IsAnthropicOAuthMode() bool       { return r.anthropicOAuthMode }
func (r *RemoteProxy) IsCaseProxy() bool                { return r.caseProxy }
func (r *RemoteProxy) AuthProfile() string              { return r.profileName }
func (r *RemoteProxy) ProfileConnectionVersion() string { return r.profileConnection }
func (r *RemoteProxy) SupportsTokenIdentity() bool      { return r.identityMode }

func (r *RemoteProxy) IssueToken(containerName string, ttlSec int) (string, error) {
	return r.IssueTokenWithIdentity(containerName, ttlSec, identity.Identity{})
}

func (r *RemoteProxy) IssueTokenWithIdentity(containerName string, ttlSec int, id identity.Identity) (string, error) {
	return r.IssueTokenWithPolicy(containerName, ttlSec, id, nil)
}
func (r *RemoteProxy) IssueTokenWithPolicy(containerName string, ttlSec int, id identity.Identity, policy *identity.ModelPolicy) (string, error) {
	return r.IssueTokenWithUsage(containerName, ttlSec, id, policy, nil)
}

func (r *RemoteProxy) IssueTokenWithUsage(containerName string, ttlSec int, id identity.Identity, policy *identity.ModelPolicy, limits *identity.UsagePolicy) (string, error) {
	if err := limits.Validate(); err != nil {
		return "", err
	}
	if limits != nil && !r.tokenUsagePolicy {
		return "", fmt.Errorf("auth proxy does not support durable execution usage limits")
	}
	if err := policy.Validate(); err != nil {
		return "", err
	}
	if policy != nil && !r.tokenModelPolicy {
		return "", fmt.Errorf("auth proxy does not support token model policy")
	}
	if !id.Empty() && !r.identityMode {
		return "", fmt.Errorf("auth proxy does not support token identity; rebuild it before launching this worker")
	}
	body, _ := json.Marshal(map[string]interface{}{"container": containerName, "ttl": ttlSec, "identity": id, "model_policy": policy, "usage_policy": limits})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, r.adminURL+"/admin/issue", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if r.adminSecret != "" {
		req.Header.Set(r.adminSecretHeader, r.adminSecret)
	}
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("issue token failed: status %d", resp.StatusCode)
	}
	var out map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out["token"], nil
}

func (r *RemoteProxy) RevokeToken(containerName string) { _ = r.RevokeTokenChecked(containerName) }

func (r *RemoteProxy) RevokeTokenChecked(containerName string) error {
	u, _ := url.Parse(r.adminURL + "/admin/revoke")
	q := u.Query()
	q.Set("container", containerName)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, u.String(), nil)
	if err != nil {
		return err
	}
	if r.adminSecret != "" {
		req.Header.Set(r.adminSecretHeader, r.adminSecret)
	}
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("proxy token revocation failed: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (r *RemoteProxy) RevokeRun(ctx context.Context, id identity.Identity) error {
	if err := id.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(id)
	if err != nil {
		return err
	}
	return r.revokeIdentity(ctx, "/admin/revoke-run", data)
}

func (r *RemoteProxy) RevokeCase(ctx context.Context) error {
	return r.revokeIdentity(ctx, "/admin/revoke-case", nil)
}

func (r *RemoteProxy) revokeIdentity(ctx context.Context, path string, body []byte) error {
	if !r.identityMode {
		return fmt.Errorf("auth proxy does not support identity revocation")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.adminURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if r.adminSecret != "" {
		req.Header.Set(r.adminSecretHeader, r.adminSecret)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("identity revocation failed: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (r *RemoteProxy) fetchMode(ctx context.Context) (map[string]bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.adminURL+"/admin/mode", nil)
	if err != nil {
		return nil, err
	}
	if r.adminSecret != "" {
		req.Header.Set(r.adminSecretHeader, r.adminSecret)
	}
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connecting remote auth proxy (%s): %w%s", r.adminURL, err, connectionHint(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("checking remote auth proxy mode failed: status %d", resp.StatusCode)
	}
	var out map[string]bool
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out["independent_profile"] {
		r.profileName = resp.Header.Get("X-Cxd-Auth-Profile")
		r.profileConnection = resp.Header.Get("X-Cxd-Connection-Version")
		if credentialstore.ValidateProfileName(r.profileName) != nil || r.profileConnection == "" || out["case_proxy"] {
			return nil, fmt.Errorf("auth proxy has incomplete independent profile metadata")
		}
	}
	return out, nil
}

func connectionHint(err error) string {
	var netErr *net.OpError
	if !errors.As(err, &netErr) {
		return ""
	}
	if !strings.Contains(strings.ToLower(netErr.Err.Error()), "connection refused") {
		return ""
	}
	return "\nHint: auth proxy is not running. Run `oaiso init` or `oaiso proxy run` on the prepared containerd host."
}

func (r *RemoteProxy) SupportsCredentialBroker() bool { return r.credentialBroker }

func (r *RemoteProxy) SupportsModelAuthorization() bool { return r.modelAuthorization }
func (r *RemoteProxy) ModelPolicyActive() bool          { return r.modelPolicyActive }

func (r *RemoteProxy) SupportsTokenModelPolicy() bool { return r.tokenModelPolicy }
