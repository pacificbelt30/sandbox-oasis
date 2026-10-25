package kataguard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func customProfile() ResourceProfile {
	return ResourceProfile{Name: "batch", CPUCores: 2, GuestMemoryBytes: SmallGuestMemory, HostMemoryBytes: SmallHostMemory, PIDLimit: 256}
}
func TestOperatorProfilesKeepPinnedBoundsAndBuiltinDefaults(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "profiles.json")
	p := customProfile()
	data, err := json.Marshal([]ResourceProfile{p})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	profiles, err := loadResourceProfiles(path)
	if err != nil || len(profiles) != 1 || !profiles[0].Equal(p) {
		t.Fatalf("profiles: %v %v", profiles, err)
	}
	if p.CPUQuota() != "200000 100000" {
		t.Fatal("custom CPU ceiling was rounded")
	}
	if len(ResourceProfiles()) != 3 || CurrentResourceProfile().CPUCores != 1 {
		t.Fatal("custom profiles mutated global defaults")
	}
	for _, mutate := range []func(*ResourceProfile){
		func(p *ResourceProfile) { p.Name = "standard" }, func(p *ResourceProfile) { p.Name = "../batch" }, func(p *ResourceProfile) { p.CPUCores = 0 }, func(p *ResourceProfile) { p.CPUCores = 65 }, func(p *ResourceProfile) { p.PIDLimit = 0 }, func(p *ResourceProfile) { p.PIDLimit = 4097 }, func(p *ResourceProfile) { p.GuestMemoryBytes = 256 << 20 }, func(p *ResourceProfile) { p.GuestMemoryBytes++ }, func(p *ResourceProfile) { p.HostMemoryBytes = p.GuestMemoryBytes }, func(p *ResourceProfile) { p.HostMemoryBytes = 4 << 30 },
	} {
		v := p
		mutate(&v)
		if err := ValidateCustomResourceProfile(v); err == nil {
			t.Fatalf("accepted invalid profile: %+v", v)
		}
	}
	for _, bad := range []string{string(data) + "{}", `[{"name":"batch","unknown":true}]`, "[" + string(data[1:len(data)-1]) + "," + string(data[1:len(data)-1]) + "]"} {
		if err := os.WriteFile(path, []byte(bad), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadResourceProfiles(path); err == nil {
			t.Fatal("invalid registry accepted")
		}
	}
	if err := os.WriteFile(path, data, 0666); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0666); err != nil {
		t.Fatal(err)
	}
	if _, err := loadResourceProfiles(path); err == nil {
		t.Fatal("writable operator registry was trusted")
	}
}
