package sandbox

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Heartbeat renews an operator-controlled idle-protection lease. No guest socket
// is exposed. Stopped notifications expire; maximum lifetime is independent.
func (m *Manager) Heartbeat(ctx context.Context, worker string, ttl time.Duration) (time.Time, error) {
	if ttl < 0 || ttl > 5*time.Minute || ttl%time.Second != 0 {
		return time.Time{}, fmt.Errorf("activity TTL must be a whole number of seconds between 0 and 300")
	}
	v, err := m.cli.ContainerInspect(ctx, worker)
	if err != nil {
		return time.Time{}, err
	}
	var result struct {
		Worker string    `json:"worker"`
		Until  time.Time `json:"active_until"`
	}
	path := "/containers/" + url.PathEscape(v.ID) + "/activity?ttl=" + fmt.Sprint(int(ttl/time.Second))
	if err := m.jobRequest(ctx, http.MethodPost, path, &result); err != nil {
		return time.Time{}, err
	}
	if result.Worker != v.ID || result.Until.IsZero() {
		return time.Time{}, fmt.Errorf("activity renewal was not confirmed by the node")
	}
	return result.Until, nil
}
