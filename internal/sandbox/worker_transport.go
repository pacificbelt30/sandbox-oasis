package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/pacificbelt30/codex-dock/internal/authproxy"
)

func (m *Manager) restoreWorkerToken(v container.InspectResponse) error {
	token := workerPlaceholder(v)
	if token == "" {
		return nil
	}
	proxy, err := m.workerProxy(v)
	if err != nil {
		return err
	}
	transport, available := proxy.(authproxy.WorkerTransportService)
	secure := available && transport.WorkerTransportEnabled()
	saved := false
	for _, env := range v.Config.Env {
		if env == "CODEX_DOCK_WORKER_MTLS=1" {
			saved = true
		}
	}
	if secure != saved {
		return fmt.Errorf("worker/proxy TLS mode changed; recreate the worker")
	}
	if err = m.authorizeWorkerToken(v); err != nil {
		return err
	}
	if !secure {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err = m.installWorkerTransport(ctx, v, transport, proxy.ContainerEndpoint(), token)
	if err != nil {
		proxy.RevokeToken(strings.TrimPrefix(v.Name, "/"))
		return err
	}
	return nil
}
func (m *Manager) installWorkerTransport(ctx context.Context, v container.InspectResponse, service authproxy.WorkerTransportService, endpoint, token string) error {
	ttl, _ := strconv.Atoi(v.Config.Labels[labelPrefix+"token-ttl"])
	if ttl <= 0 {
		ttl = 3600
	}
	config, err := service.WorkerTransport(ctx, token, ttl)
	if err != nil {
		return err
	}
	if v.HostConfig == nil || len(v.HostConfig.ExtraHosts) == 0 {
		return fmt.Errorf("worker TLS requires verified proxy host mappings")
	}
	hosts := v.HostConfig.ExtraHosts
	config.Endpoint, err = kataProxyEndpoint(endpoint, hosts)
	if err != nil {
		return err
	}
	if _, err = config.TLSConfig(); err != nil {
		return err
	}
	data, err := json.Marshal(config)
	if err != nil {
		return err
	}
	defer clear(data)
	_, err = m.execTrustedInput(ctx, v.ID, []string{guestGuard, "transport-install"}, bytes.NewReader(data))
	return err
}
