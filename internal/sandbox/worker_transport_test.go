package sandbox

import (
	"context"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/pacificbelt30/codex-dock/internal/workertransport"
)

type fixtureWorkerTransport struct{ authority *workertransport.Authority }

func (f fixtureWorkerTransport) WorkerTransportEnabled() bool { return true }
func (f fixtureWorkerTransport) WorkerTransport(_ context.Context, token string, ttl int) (workertransport.Config, error) {
	return f.authority.Issue(token, ttl)
}
func TestTransportUsesVerifiedIPMappingsRatherThanSavedProxyNames(t *testing.T) {
	a, err := workertransport.NewAuthority()
	if err != nil {
		t.Fatal(err)
	}
	m := &Manager{execProbe: func(_ context.Context, _ string, argv []string) ([]byte, error) {
		if len(argv) != 2 || argv[0] != guestGuard || argv[1] != "transport-install" {
			t.Fatal("transport material entered argv")
		}
		return nil, nil
	}}
	v := container.InspectResponse{ContainerJSONBase: &container.ContainerJSONBase{ID: "worker", HostConfig: &container.HostConfig{ExtraHosts: []string{"auth:192.0.2.2"}}}, Config: &container.Config{Labels: map[string]string{labelProxyHosts: `["auth"]`}}}
	if err = m.installWorkerTransport(context.Background(), v, fixtureWorkerTransport{a}, "https://auth:18080", "cdx-"+strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	v.HostConfig.ExtraHosts = nil
	if err = m.installWorkerTransport(context.Background(), v, fixtureWorkerTransport{a}, "https://auth:18080", "cdx-"+strings.Repeat("a", 64)); err == nil {
		t.Fatal("unmapped proxy accepted")
	}
}
