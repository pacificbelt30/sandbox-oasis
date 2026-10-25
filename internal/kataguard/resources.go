package kataguard

import (
	"errors"
	"slices"
)

var ErrCapacity = errors.New("kata capacity exhausted")

// ResourceProfile is the pinned capacity contract for one Kata worker.
// HostMemoryBytes is a reservation ceiling, not measured RSS.
type ResourceProfile struct {
	Name                string    `json:"name"`
	CPUCores            int       `json:"cpu_cores"`
	GuestMemoryBytes    int64     `json:"guest_memory_bytes"`
	HostMemoryBytes     int64     `json:"host_memory_bytes"`
	PIDLimit            int64     `json:"pid_limit"`
	FileDescriptorLimit uint64    `json:"file_descriptor_limit,omitempty"`
	BlockIO             []IOLimit `json:"block_io,omitempty"`
}

const SmallGuestMemory = int64(1024 * 1024 * 1024)
const SmallHostMemory = int64(2048 * 1024 * 1024)
const CompactGuestMemory = int64(512 * 1024 * 1024)
const CompactHostMemory = int64(768 * 1024 * 1024)

func CurrentResourceProfile() ResourceProfile {
	return ResourceProfile{
		Name:             "standard",
		CPUCores:         1,
		GuestMemoryBytes: GuestMemory,
		HostMemoryBytes:  HostMemory,
		PIDLimit:         512,
	}
}

func LookupResourceProfile(name string) (ResourceProfile, bool) {
	switch name {
	case "", "standard":
		return CurrentResourceProfile(), true
	case "small":
		return ResourceProfile{Name: "small", CPUCores: 1, GuestMemoryBytes: SmallGuestMemory, HostMemoryBytes: SmallHostMemory, PIDLimit: 512}, true
	case "compact":
		return ResourceProfile{Name: "compact", CPUCores: 1, GuestMemoryBytes: CompactGuestMemory, HostMemoryBytes: CompactHostMemory, PIDLimit: 512}, true
	default:
		return ResourceProfile{}, false
	}
}

func ResourceProfiles() []ResourceProfile {
	standard := CurrentResourceProfile()
	small, _ := LookupResourceProfile("small")
	compact, _ := LookupResourceProfile("compact")
	return []ResourceProfile{standard, small, compact}
}

func (p ResourceProfile) Equal(other ResourceProfile) bool {
	return p.Name == other.Name && p.CPUCores == other.CPUCores && p.GuestMemoryBytes == other.GuestMemoryBytes && p.HostMemoryBytes == other.HostMemoryBytes && p.PIDLimit == other.PIDLimit && p.FileDescriptorLimit == other.FileDescriptorLimit && slices.Equal(p.BlockIO, other.BlockIO)
}
