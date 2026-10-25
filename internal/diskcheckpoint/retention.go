package diskcheckpoint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

const PinLabel = "io.oaiso.checkpoint.pinned"

type RetentionOptions struct {
	KeepBytes     int64  `json:"keep_bytes"`
	MaxAgeSeconds int64  `json:"max_age_seconds"`
	Apply         bool   `json:"apply"`
	ExpectedPlan  string `json:"expected_plan,omitempty"`
}
type RetentionPlan struct {
	ID             string   `json:"id"`
	Delete         []string `json:"delete"`
	Protected      []string `json:"protected"`
	UsedBytes      int64    `json:"used_bytes"`
	RemainingBytes int64    `json:"remaining_bytes"`
	TargetMet      bool     `json:"target_met"`
	Removed        []string `json:"removed"`
}
type PinRequest struct {
	ID     string `json:"id"`
	Pinned bool   `json:"pinned"`
}

// Bucket the clock to a minute so plan/apply can be reviewed. Recompute on
// application: boundary crossings require another explicit review.
func PlanRetention(c Catalog, o RetentionOptions, inUse map[string]bool, now time.Time) (RetentionPlan, error) {
	if o.KeepBytes < 0 || o.KeepBytes > CapacityBytes || o.MaxAgeSeconds < 0 || o.MaxAgeSeconds > 315360000 || o.KeepBytes == 0 && o.MaxAgeSeconds == 0 {
		return RetentionPlan{}, fmt.Errorf("set a keep-bytes target or max-age (up to ten years)")
	}
	p := RetentionPlan{Delete: []string{}, Protected: []string{}, UsedBytes: c.UsedBytes, RemainingBytes: c.UsedBytes, Removed: []string{}}
	items := append([]Info(nil), c.Items...)
	sort.Slice(items, func(i, j int) bool {
		if items[i].Created.Equal(items[j].Created) {
			return items[i].ID < items[j].ID
		}
		return items[i].Created.Before(items[j].Created)
	})
	now = now.UTC().Truncate(time.Minute)
	for _, v := range items {
		if v.Pinned || inUse[v.ID] {
			p.Protected = append(p.Protected, v.ID)
			continue
		}
		expired := o.MaxAgeSeconds > 0 && !v.Created.Add(time.Duration(o.MaxAgeSeconds)*time.Second).After(now)
		if expired || o.KeepBytes > 0 && p.RemainingBytes > o.KeepBytes {
			p.Delete = append(p.Delete, v.ID)
			p.RemainingBytes -= v.Bytes
		}
	}
	p.TargetMet = o.KeepBytes == 0 || p.RemainingBytes <= o.KeepBytes
	o.Apply = false
	o.ExpectedPlan = ""
	data, _ := json.Marshal(struct {
		Catalog Catalog
		Options RetentionOptions
		Plan    RetentionPlan
	}{c, o, p})
	sum := sha256.Sum256(data)
	p.ID = hex.EncodeToString(sum[:])
	return p, nil
}
