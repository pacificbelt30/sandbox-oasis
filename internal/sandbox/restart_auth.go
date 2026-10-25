package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/pacificbelt30/codex-dock/internal/authproxy"
	"github.com/pacificbelt30/codex-dock/pkg/cxd/identity"
)

func (m *Manager) workerProxy(v container.InspectResponse) (authproxy.Service, error) {
	return m.workerProxyContext(context.Background(), v)
}
func (m *Manager) workerProxyContext(ctx context.Context, v container.InspectResponse) (authproxy.Service, error) {
	if m.proxy != nil {
		return m.proxy, nil
	}
	if m.proxyConnectorContext != nil {
		return m.proxyConnectorContext(ctx, v.Config.Labels[labelPrefix+"proxy-admin-url"], v.Config.Labels[labelPrefix+"proxy-container-url"])
	}
	if m.proxyConnector == nil {
		return nil, fmt.Errorf("auth proxy connection is required to restart this worker")
	}
	return m.proxyConnector(v.Config.Labels[labelPrefix+"proxy-admin-url"], v.Config.Labels[labelPrefix+"proxy-container-url"])
}

func workerPlaceholder(v container.InspectResponse) string {
	if v.Config == nil {
		return ""
	}
	for _, entry := range v.Config.Env {
		key, value, ok := strings.Cut(entry, "=")
		if ok && (key == "CODEX_TOKEN" || key == "ANTHROPIC_API_KEY" || key == "CLAUDE_CODE_OAUTH_TOKEN") && strings.HasPrefix(value, "cdx-") {
			return value
		}
	}
	return ""
}

func (m *Manager) restoreWorkerToken(v container.InspectResponse) error {
	token := workerPlaceholder(v)
	if token == "" {
		return nil
	}
	p, err := m.workerProxy(v)
	if err != nil {
		return err
	}
	if err := checkWorkerProfile(p, v.Config.Labels); err != nil {
		return err
	}
	r, ok := p.(authproxy.TokenRestorer)
	if !ok {
		return fmt.Errorf("auth proxy does not support worker restart; update the proxy")
	}
	ttl, _ := strconv.Atoi(v.Config.Labels[labelPrefix+"token-ttl"])
	if ttl <= 0 {
		ttl = 3600
	}
	var id identity.Identity
	if raw := v.Config.Labels[labelPrefix+"token-identity"]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &id); err != nil {
			return fmt.Errorf("invalid saved worker token identity: %w", err)
		}
		if err := id.Validate(); err != nil {
			return err
		}
	}
	if raw := v.Config.Labels[labelPrefix+"token-usage-policy"]; raw != "" {
		var limits identity.UsagePolicy
		if err := json.Unmarshal([]byte(raw), &limits); err != nil {
			return fmt.Errorf("invalid saved usage policy")
		}
		if err := limits.Validate(); err != nil {
			return err
		}
		var model *identity.ModelPolicy
		if raw := v.Config.Labels[labelPrefix+"token-model-policy"]; raw != "" {
			model = &identity.ModelPolicy{}
			if err := json.Unmarshal([]byte(raw), model); err != nil {
				return fmt.Errorf("invalid saved model policy")
			}
			if err := model.Validate(); err != nil {
				return err
			}
		}
		restorer, ok := p.(authproxy.UsageRestorer)
		if !ok {
			return fmt.Errorf("auth proxy does not support usage policy restore")
		}
		return restorer.RestoreTokenWithUsage(strings.TrimPrefix(v.Name, "/"), token, ttl, id, model, &limits)
	}
	if raw := v.Config.Labels[labelPrefix+"token-model-policy"]; raw != "" {
		var policy identity.ModelPolicy
		if err := json.Unmarshal([]byte(raw), &policy); err != nil {
			return fmt.Errorf("invalid saved model policy: %w", err)
		}
		if err := policy.Validate(); err != nil {
			return err
		}
		restorer, ok := p.(authproxy.PolicyRestorer)
		if !ok {
			return fmt.Errorf("auth proxy does not support token model policy restore")
		}
		return restorer.RestoreTokenWithPolicy(strings.TrimPrefix(v.Name, "/"), token, ttl, id, &policy)
	}
	if !id.Empty() {
		identityRestorer, ok := p.(authproxy.IdentityRestorer)
		if !ok {
			return fmt.Errorf("auth proxy does not support token identity restore")
		}
		return identityRestorer.RestoreTokenWithIdentity(strings.TrimPrefix(v.Name, "/"), token, ttl, id)
	}
	return r.RestoreToken(strings.TrimPrefix(v.Name, "/"), token, ttl)
}

func checkWorkerProfile(p authproxy.Service, labels map[string]string) error {
	saved, connection := labels[labelPrefix+"auth-profile"], labels[labelPrefix+"auth-profile-connection"]
	var current, currentConnection string
	if profile, ok := p.(authproxy.ProfileService); ok {
		current, currentConnection = profile.AuthProfile(), profile.ProfileConnectionVersion()
	}
	if saved == "" && connection == "" && current == "" {
		return nil
	}
	if saved == "" || connection == "" || saved != current || connection != currentConnection {
		return fmt.Errorf("worker authentication profile or login changed; create a new worker")
	}
	return nil
}

func (m *Manager) revokeWorkerToken(v container.InspectResponse) {
	if err := m.revokeWorkerTokenChecked(v); err != nil {
		fmt.Fprintln(os.Stderr, "warning: worker token could not be revoked:", err)
	}
}

func (m *Manager) revokeWorkerTokenChecked(v container.InspectResponse) error {
	if workerPlaceholder(v) == "" {
		return nil
	}
	p, err := m.workerProxy(v)
	if err != nil {
		return err
	}
	name := strings.TrimPrefix(v.Name, "/")
	if revoker, ok := p.(authproxy.TokenRevoker); ok {
		return revoker.RevokeTokenChecked(name)
	}
	p.RevokeToken(name)
	return nil
}
