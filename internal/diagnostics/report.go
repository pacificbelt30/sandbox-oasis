// Package diagnostics produces actionable, secret-free readiness reports.
package diagnostics

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}
type Report struct {
	Checks []Check `json:"checks"`
}

func (r *Report) Add(name, detail, fix string, err error) {
	c := Check{Name: name, Status: "ok", Detail: detail}
	if err != nil {
		c.Status = "error"
		c.Detail = err.Error()
		c.Fix = fix
	}
	r.Checks = append(r.Checks, c)
}
func (r Report) Err() error {
	var failed []string
	for _, c := range r.Checks {
		if c.Status == "error" {
			failed = append(failed, c.Name)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("readiness checks failed: %s", strings.Join(failed, ", "))
	}
	return nil
}
func (r Report) Write(w io.Writer, asJSON bool) error {
	if asJSON {
		e := json.NewEncoder(w)
		e.SetIndent("", "  ")
		return e.Encode(r)
	}
	for _, c := range r.Checks {
		if _, err := fmt.Fprintf(w, "[%s] %s: %s\n", c.Status, c.Name, c.Detail); err != nil {
			return err
		}
		if c.Fix != "" {
			if _, err := fmt.Fprintf(w, "  Fix: %s\n", c.Fix); err != nil {
				return err
			}
		}
	}
	return nil
}

// Hint preserves the actual failure while selecting recovery steps by subsystem.
func Hint(err error) string {
	if err == nil {
		return ""
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "quota"), strings.Contains(s, "rootfs"), strings.Contains(s, "storage capacity"), strings.Contains(s, "xfs"):
		return "Run oaiso runtime-check; use dedicated writable XFS with project quota accounting and enforcement. Back up stopped workers before migration; see doc/guides/storage-migration.md. Do not format existing data."
	case strings.Contains(s, "proxy image"), strings.Contains(s, "image authority"), strings.Contains(s, "trusted image"), strings.Contains(s, "proxy trust"):
		return "Run oaiso proxy build to rebuild the trusted image. For a custom image, review its source and digest before explicit oaiso proxy trust IMAGE; drain workers before replacing a running proxy."
	case strings.Contains(s, "permission denied"):
		return "Run id and ls -l /run/codex-dock-node.sock; join the codex-dock group and log in again. Do not chmod the socket to 0666."
	case strings.Contains(s, "shared workspace"), strings.Contains(s, "virtio_fs"), strings.Contains(s, "manifest"), strings.Contains(s, "checksum"), strings.Contains(s, "profile"):
		return "Run oaiso setup --bundle PATH --operator USER to review the installed profile, then oaiso setup --bundle PATH --operator USER --apply as root; see doc/setup.md."
	case strings.Contains(s, "cgroup"):
		return "Check stat -fc %T /sys/fs/cgroup and systemctl status codex-dock-node; cpu, memory and pids must be delegated on cgroup v2."
	default:
		return "Run systemctl status codex-dock-containerd codex-dock-node and journalctl -u codex-dock-node -n 50; use oaiso setup for a new host."
	}
}
