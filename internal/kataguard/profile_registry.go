package kataguard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
)

var resourceProfileName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// CPUQuota is the exact cgroup-v2 CPU ceiling used on host and guest.
func (p ResourceProfile) CPUQuota() string { return fmt.Sprintf("%d 100000", int64(p.CPUCores)*100000) }

// Custom profiles stay inside the pinned Dragonball asset envelope. Larger
// guests require a separately validated asset/configuration profile.
func ValidateCustomResourceProfile(p ResourceProfile) error {
	const mib = int64(1 << 20)
	if !resourceProfileName.MatchString(p.Name) {
		return fmt.Errorf("invalid resource profile name")
	}
	if _, builtin := LookupResourceProfile(p.Name); builtin {
		return fmt.Errorf("built-in resource profiles cannot be replaced")
	}
	if p.CPUCores < 1 || p.CPUCores > 64 || p.GuestMemoryBytes < CompactGuestMemory || p.GuestMemoryBytes > GuestMemory || p.GuestMemoryBytes%mib != 0 || p.HostMemoryBytes < p.GuestMemoryBytes+256*mib || p.HostMemoryBytes > HostMemory || p.HostMemoryBytes%mib != 0 || p.PIDLimit < 64 || p.PIDLimit > 4096 {
		return fmt.Errorf("resource profile exceeds pinned CPU/memory/PID bounds")
	}
	return ValidateAdditionalLimits(p)
}

func loadResourceProfiles(path string) ([]ResourceProfile, error) {
	if path == "" {
		return nil, nil
	}
	if err := trustedPath(path, false); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 64<<10 {
		return nil, fmt.Errorf("resource profiles file exceeds 64 KiB")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var profiles []ResourceProfile
	if err := d.Decode(&profiles); err != nil {
		return nil, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("trailing resource profile data")
	}
	if len(profiles) > 32 {
		return nil, fmt.Errorf("at most 32 custom resource profiles are supported")
	}
	seen := make(map[string]bool)
	for _, p := range profiles {
		if err := ValidateCustomResourceProfile(p); err != nil {
			return nil, err
		}
		if seen[p.Name] {
			return nil, fmt.Errorf("duplicate resource profile")
		}
		seen[p.Name] = true
	}
	return profiles, nil
}

func (g *Guard) resourceProfile(name string) (ResourceProfile, bool) {
	if p, ok := LookupResourceProfile(name); ok {
		return p, true
	}
	for _, p := range g.profiles {
		if p.Name == name {
			return p, true
		}
	}
	return ResourceProfile{}, false
}
func (g *Guard) resourceProfiles() []ResourceProfile {
	return append(ResourceProfiles(), g.profiles...)
}
