// Package storageinfo describes host-observed storage, never guest-reported df values.
package storageinfo

import "time"

type Usage struct {
	Available   bool      `json:"available"`
	ReadOnly    bool      `json:"read_only"`
	UsedBytes   uint64    `json:"used_bytes"`
	LimitBytes  uint64    `json:"limit_bytes"`
	UsedInodes  uint64    `json:"used_inodes"`
	LimitInodes uint64    `json:"limit_inodes"`
	ObservedAt  time.Time `json:"observed_at"`
	Error       string    `json:"error,omitempty"`
}
type Status struct {
	Enforced          bool   `json:"enforced"`
	TotalBytes        uint64 `json:"total_bytes"`
	AvailableBytes    uint64 `json:"available_bytes"`
	WorkerLimitBytes  uint64 `json:"worker_limit_bytes"`
	WorkerLimitInodes uint64 `json:"worker_limit_inodes"`
	Error             string `json:"error,omitempty"`
}
