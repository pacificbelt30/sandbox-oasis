//go:build linux

package engine

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/pacificbelt30/codex-dock/internal/workerpolicy"
)

func TestIdleCollectionProtectsPendingStreamingAndRecoveredJobs(t *testing.T) {
	b := &Backend{execs: map[string]*execution{
		"pending": {Container: "worker"},
		"stream":  {Container: "worker", Started: true, Running: true},
		"job":     {Container: "worker", Started: true, Running: true, Detached: true},
		"done":    {Container: "worker", Started: true},
		"other":   {Container: "other", Running: true},
	}}
	before := time.Now().Add(-time.Hour)
	last := before.Add(-time.Hour)
	if n := b.activeExecutions("worker"); n != 3 || idleEligible(last, before, n) {
		t.Fatalf("active sessions not protected: %d", n)
	}
	delete(b.execs, "pending")
	b.execs["stream"].Running = false
	b.execs["job"].Running = false
	if n := b.activeExecutions("worker"); n != 0 || !idleEligible(last, before, n) {
		t.Fatal("completed sessions block collection")
	}
	if idleEligible(before.Add(time.Second), before, 0) || idleEligible(time.Time{}, before, 0) {
		t.Fatal("new or unknown activity was collected")
	}
}

func TestMainActivityAndRenewalSurviveRecordReload(t *testing.T) {
	now := time.Now().UTC()
	policy, _ := json.Marshal(workerpolicy.Lifecycle{ProtectMain: true, IdleTimeout: time.Second})
	r := record{ID: "worker", LastActivity: now.Add(-time.Hour), Config: container.Config{Labels: map[string]string{"codex-dock.lifecycle": string(policy)}}}
	b := &Backend{}
	if active, err := b.activeWork(&r, true, now); err != nil || active != 1 || idleEligible(r.LastActivity, now, active) {
		t.Fatalf("running primary was not protected: %d %v", active, err)
	}
	if active, err := b.activeWork(&r, false, now); err != nil || active != 0 {
		t.Fatalf("finished primary remains active: %d %v", active, err)
	}
	r.ActivityUntil = now.Add(time.Minute)
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var restored record
	if err = json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if active, err := b.activeWork(&restored, false, now); err != nil || active != 1 {
		t.Fatalf("renewal lost on node restart: %d %v", active, err)
	}
	if active, err := b.activeWork(&restored, false, now.Add(time.Minute)); err != nil || active != 0 {
		t.Fatalf("expired notification still protects idle work: %d %v", active, err)
	}
	restored.Config.Labels["codex-dock.lifecycle"] = "invalid"
	if _, err := b.activeWork(&restored, true, now); err == nil {
		t.Fatal("unknown lifecycle was considered idle")
	}
}
