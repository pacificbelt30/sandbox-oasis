package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type countedExecNode struct {
	*fakeKataNode
	validated, verified, combined int
	failure                       error
}

func (n *countedExecNode) Validate(context.Context, string) error { n.validated++; return n.failure }
func (n *countedExecNode) Verify(context.Context, string, string) error {
	n.verified++
	return n.failure
}

type combinedExecNode struct {
	*countedExecNode
	ready bool
}

func (n *combinedExecNode) Stamp() string {
	if !n.ready {
		return ""
	}
	return n.fakeKataNode.Stamp()
}
func (n *combinedExecNode) Parent(id string) (string, error) {
	if !n.ready {
		return "", errors.New("no validated node metadata")
	}
	return n.fakeKataNode.Parent(id)
}
func (n *combinedExecNode) Launcher() string {
	if !n.ready {
		return ""
	}
	return n.fakeKataNode.Launcher()
}

func (n *combinedExecNode) VerifyRuntime(_ context.Context, runtime, allocation, worker string) error {
	n.combined++
	if runtime != DefaultKataRuntime || len(allocation) != 32 || worker == "" {
		return errors.New("invalid attestation identity")
	}
	if n.failure == nil {
		n.ready = true
	}
	return n.failure
}

func TestExecVerificationRetainsAllGates(t *testing.T) {
	for _, combined := range []bool{false, true} {
		name := "legacy"
		if combined {
			name = "combined"
		}
		t.Run(name, func(t *testing.T) {
			f, m := newEngineFixture(t)
			id, err := m.Run(RunOptions{Name: "verified-exec", ProjectDir: t.TempDir(), Runtime: RuntimeKata, Detach: true})
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/containers/json") {
					_ = json.NewEncoder(w).Encode([]container.Summary{{ID: id}})
					return
				}
				f.serve(w, r)
			}))
			defer server.Close()
			cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.51"), client.WithHTTPClient(server.Client()))
			if err != nil {
				t.Fatal(err)
			}
			defer cli.Close()
			m.cli = cli
			n := &countedExecNode{fakeKataNode: &fakeKataNode{}}
			if combined {
				m.guard = &combinedExecNode{countedExecNode: n}
			} else {
				m.guard = n
			}
			originalProbe := m.execProbe
			probes := 0
			m.execProbe = func(ctx context.Context, id string, args []string) ([]byte, error) {
				probes++
				return originalProbe(ctx, id, args)
			}
			for range 2 {
				if _, err = m.verifyExecWorker(context.Background(), id); err != nil {
					t.Fatal(err)
				}
			}
			if probes != 2 {
				t.Fatalf("guest checks skipped: %d", probes)
			}
			if combined {
				if n.combined != 2 || n.validated != 0 || n.verified != 0 {
					t.Fatalf("unexpected verification: %+v", n)
				}
			} else if n.validated != 2 || n.verified != 2 {
				t.Fatalf("legacy checks skipped: %+v", n)
			}
			n.failure = errors.New("changed assets or host")
			if _, err = m.verifyExecWorker(context.Background(), id); err == nil || probes != 2 {
				t.Fatalf("failed attestation allowed guest probe: %v probes=%d", err, probes)
			}
			n.failure = nil
			f.worker.HostConfig.Memory++
			if _, err = m.verifyExecWorker(context.Background(), id); err == nil || probes != 2 {
				t.Fatalf("changed configuration accepted: %v", err)
			}
			f.worker.HostConfig.Memory--
			m.execProbe = func(context.Context, string, []string) ([]byte, error) { return nil, errors.New("guest probe failed") }
			if _, err = m.verifyExecWorker(context.Background(), id); err == nil {
				t.Fatal("failed guest probe accepted")
			}
		})
	}
}
