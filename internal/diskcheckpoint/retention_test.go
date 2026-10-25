package diskcheckpoint

import (
	"testing"
	"time"
)

func TestRetentionProtectsReferencesAndPinsAndDetectsChangedPlans(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	c := Catalog{UsedBytes: 300, Items: []Info{{ID: "old", Bytes: 100, Created: now.Add(-time.Hour)}, {ID: "pin", Bytes: 100, Created: now.Add(-time.Hour), Pinned: true}, {ID: "used", Bytes: 100, Created: now.Add(-time.Hour)}}}
	o := RetentionOptions{KeepBytes: 1, MaxAgeSeconds: 10}
	p, err := PlanRetention(c, o, map[string]bool{"used": true}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Delete) != 1 || p.Delete[0] != "old" || len(p.Protected) != 2 || p.TargetMet || p.RemainingBytes != 200 {
		t.Fatalf("unsafe plan: %+v", p)
	}
	o.Apply = true
	o.ExpectedPlan = p.ID
	same, err := PlanRetention(c, o, map[string]bool{"used": true}, now)
	if err != nil || same.ID != p.ID {
		t.Fatal("review/apply changed plan")
	}
	changed, _ := PlanRetention(c, o, map[string]bool{"old": true, "used": true}, now)
	if changed.ID == p.ID {
		t.Fatal("new reference did not invalidate plan")
	}
	c.Items[0].Pinned = true
	changed, _ = PlanRetention(c, o, map[string]bool{"used": true}, now)
	if changed.ID == p.ID {
		t.Fatal("new pin did not invalidate plan")
	}
	if _, err = PlanRetention(c, RetentionOptions{}, nil, now); err == nil {
		t.Fatal("unbounded deletion accepted")
	}
}
