package identity

import "fmt"

// UsagePolicy is immutable per execution. Zero fields are unlimited; nil turns
// metering off. Tokens are observed upstream usage, not a prepaid money cap.
type UsagePolicy struct {
	MaxRequests   int64 `json:"max_requests,omitempty"`
	MaxConcurrent int64 `json:"max_concurrent,omitempty"`
	MaxBytes      int64 `json:"max_bytes,omitempty"`
	MaxTokens     int64 `json:"max_tokens,omitempty"`
}

func (p *UsagePolicy) Validate() error {
	if p == nil {
		return nil
	}
	if p.MaxRequests < 0 || p.MaxConcurrent < 0 || p.MaxBytes < 0 || p.MaxTokens < 0 || *p == (UsagePolicy{}) {
		return fmt.Errorf("usage policy needs at least one positive limit and no negative values")
	}
	return nil
}

func (p *UsagePolicy) Clone() *UsagePolicy {
	if p == nil {
		return nil
	}
	copy := *p
	return &copy
}
