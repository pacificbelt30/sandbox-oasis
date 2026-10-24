package projectconfig

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPortablePoliciesUseGuestArgvAndDurations(t *testing.T) {
	root := t.TempDir()
	data := `[run]
policy_preset = "offline"
deny_read = ["/workspace/.env"]
max_lifetime = "2h"
idle_timeout = "30m"
protect_main = true
retention = "24h"
start_hooks = [["sh", "-c", "printf ready"]]
readiness = ["test", "-f", "ready"]
readiness_timeout = "1m"
`
	if err := os.WriteFile(filepath.Join(root, Filename), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	f, l, e, err := cfg.Policies()
	if err != nil || len(f.DenyRead) != 1 || !l.ProtectMain || l.MaxLifetime != 2*time.Hour || l.IdleTimeout != 30*time.Minute || l.Retention != 24*time.Hour || e.StartHooks[0][0] != "sh" || e.Readiness[0] != "test" || e.ReadinessTimeout != time.Minute {
		t.Fatalf("%+v %+v %+v %v", f, l, e, err)
	}
}
func TestPortablePolicyValidationRejectsUnsafeOrUnknownSettings(t *testing.T) {
	for _, body := range []string{`deny_read = ["/etc/passwd"]`, `max_lifetime = "-1h"`, `max_lifetime = "forever"`, `policy_preset = "unknown"`, `readiness = [""]`, `ca_bundle = "/host/private-file"`} {
		t.Run(body, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, Filename), []byte("[run]\n"+body+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(root); err == nil {
				t.Fatal("invalid project policy accepted")
			}
		})
	}
}
