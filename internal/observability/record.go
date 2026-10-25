// Package observability exports a deliberately small, secret-free diagnostic
// schema. It excludes arbitrary error text, request contents, URLs and argv.
package observability

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/diagnostics"
	"github.com/pacificbelt30/codex-dock/internal/launchtiming"
	"github.com/pacificbelt30/codex-dock/internal/policyevents"
)

type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}
type Phase struct {
	Name     string `json:"name"`
	Duration int64  `json:"duration_ns"`
}
type Memory struct {
	Current    int64  `json:"host_current_bytes"`
	Peak       *int64 `json:"host_peak_bytes,omitempty"`
	GuestLimit int64  `json:"guest_limit_bytes"`
	HostLimit  int64  `json:"host_limit_bytes"`
	OOMKills   uint64 `json:"oom_kills"`
}
type Record struct {
	Schema     int       `json:"schema"`
	Time       time.Time `json:"time"`
	Kind       string    `json:"kind"`
	WorkerHash string    `json:"worker_hash,omitempty"`
	Status     string    `json:"status"`
	Code       string    `json:"code,omitempty"`
	Checks     []Check   `json:"checks,omitempty"`
	Duration   int64     `json:"duration_ns,omitempty"`
	Phases     []Phase   `json:"phases,omitempty"`
	Memory     *Memory   `json:"memory,omitempty"`
}

func HashID(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func newRecord(kind, status string) Record {
	return Record{Schema: 1, Time: time.Now().UTC(), Kind: kind, Status: status}
}

var checkNames = map[string]bool{"egress-policy": true, "runtime": true, "host": true, "kvm": true, "cgroups": true, "version": true, "profile": true, "connection": true, "openai-connection": true, "anthropic-connection": true, "node": true, "builder": true, "forwarding": true, "proxy": true, "workers": true, "updates": true, "other": true}
var statuses = map[string]bool{"ok": true, "error": true, "unchecked": true, "unknown": true}
var reasons = map[string]bool{"denials_unavailable": true, "domain_denied": true, "port_denied": true, "default_denied": true, "domain_not_allowed": true, "port_not_allowed": true, "invalid_address": true, "private_address": true, "address_denied": true, "address_not_allowed": true, "event_log_limit": true, "usage_limit": true, "usage_state_unavailable": true, "usage_policy_websocket_unsupported": true, "usage_policy_route_unsupported": true, "unrecognized_event": true}
var phaseNames = map[string]bool{"existing_worker": true, "proxy_setup": true, "workspace_setup": true, "request_abort": true, "worker_cleanup": true, "network_setup": true, "capacity_reservation": true, "container_create": true, "created_worker_verification": true, "environment_readiness": true, "request_validation": true, "request_claim": true, "request_replay": true, "preflight": true, "worker_configuration": true, "vm_start": true, "host_guest_verification": true, "token_activation": true, "bootstrap_release": true, "request_confirmation": true, "final_inspect": true, "cleanup": true, "other": true}

func Status(report diagnostics.Report) Record {
	r := newRecord("status", "ok")
	if len(report.Checks) == 0 {
		r.Status = "unknown"
	}
	for _, c := range report.Checks {
		name, status := c.Name, c.Status
		if !checkNames[name] {
			name = "other"
		}
		if !statuses[status] {
			status = "unknown"
		}
		if status == "error" {
			r.Status = "error"
		}
		r.Checks = append(r.Checks, Check{name, status})
	}
	return r
}

func Denial(worker string, event policyevents.Event) Record {
	r := newRecord("denial", "ok")
	r.WorkerHash = HashID(worker)
	r.Code = event.Code
	if !reasons[r.Code] {
		r.Code = "unrecognized_event"
	}
	return r
}

func Launch(worker string, duration time.Duration, phases []launchtiming.Phase, failed bool) Record {
	r := newRecord("launch", "ok")
	r.WorkerHash = HashID(worker)
	r.Duration = int64(duration)
	if failed {
		r.Status = "error"
	}
	for _, p := range phases {
		name := p.Name
		if !phaseNames[name] {
			name = "other"
		}
		r.Phases = append(r.Phases, Phase{name, int64(p.Duration)})
	}
	return r
}

func Resources(worker string, memory *Memory) Record {
	r := newRecord("resources", "ok")
	r.WorkerHash = HashID(worker)
	r.Memory = memory
	if memory == nil {
		r.Status = "unknown"
	}
	return r
}

func Unavailable(worker string) Record {
	r := newRecord("collection", "unknown")
	r.WorkerHash = HashID(worker)
	r.Code = "denials_unavailable"
	return r
}

func (r Record) Validate() error {
	if r.Schema != 1 || r.Time.IsZero() || !statuses[r.Status] || r.Duration < 0 || len(r.Checks) > 64 || len(r.Phases) > 64 {
		return fmt.Errorf("invalid diagnostic record")
	}
	switch r.Kind {
	case "status", "denial", "launch", "resources", "collection":
	default:
		return fmt.Errorf("unknown diagnostic kind")
	}
	if r.WorkerHash != "" {
		data, err := hex.DecodeString(r.WorkerHash)
		if err != nil || len(data) != 32 {
			return fmt.Errorf("invalid diagnostic identity")
		}
	}
	if r.Code != "" && !reasons[r.Code] {
		return fmt.Errorf("unknown diagnostic reason")
	}
	for _, c := range r.Checks {
		if !checkNames[c.Name] || !statuses[c.Status] {
			return fmt.Errorf("invalid diagnostic check")
		}
	}
	for _, p := range r.Phases {
		if !phaseNames[p.Name] || p.Duration < 0 {
			return fmt.Errorf("invalid diagnostic phase")
		}
	}
	if m := r.Memory; m != nil {
		if m.Current < 0 || m.GuestLimit < 0 || m.HostLimit < 0 || m.Peak != nil && *m.Peak < 0 {
			return fmt.Errorf("invalid diagnostic memory")
		}
	}
	return nil
}
