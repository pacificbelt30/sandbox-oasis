package engine

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/docker/docker/api/types/system"
	"github.com/pacificbelt30/codex-dock/internal/diskcheckpoint"
)

func TestCheckpointClientRequiresCapabilityBeforeMutation(t *testing.T) {
	for _, supported := range []bool{false, true} {
		listener, err := net.Listen("unix", filepath.Join(t.TempDir(), "node.sock"))
		if err != nil {
			t.Fatal(err)
		}
		var mutations atomic.Int64
		server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := strings.TrimPrefix(r.URL.Path, "/v1.51")
			if path == "/info" {
				info := system.Info{}
				if supported {
					info.DriverStatus = [][2]string{{"cxd.disk-checkpoint", "1"}}
				}
				_ = json.NewEncoder(w).Encode(info)
				return
			}
			if path != "/checkpoints" || r.Method != http.MethodPost {
				t.Errorf("unexpected request %s %s", r.Method, path)
				http.NotFound(w, r)
				return
			}
			mutations.Add(1)
			var body struct {
				Worker  string                 `json:"worker"`
				Options diskcheckpoint.Options `json:"options"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.Worker != "source" || body.Options.MaxBytes != 1<<20 || len(body.Options.Exclude) != 1 {
				t.Error("capture request changed")
			}
			_ = json.NewEncoder(w).Encode(diskcheckpoint.Info{ID: strings.Repeat("a", 64)})
		})}
		go func() { _ = server.Serve(listener) }()
		var out diskcheckpoint.Info
		err = Checkpoints(context.Background(), listener.Addr().String(), http.MethodPost, "", "source", diskcheckpoint.Options{MaxBytes: 1 << 20, Exclude: []string{"/opt/private"}}, &out)
		_ = server.Close()
		if supported && (err != nil || mutations.Load() != 1 || out.ID == "") {
			t.Fatal("supported node capture failed", err)
		}
		if !supported && (err == nil || mutations.Load() != 0) {
			t.Fatal("old node accepted checkpoint mutation", err)
		}
	}
}
