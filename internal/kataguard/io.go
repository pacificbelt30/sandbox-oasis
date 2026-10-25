package kataguard

import (
	"fmt"
	"strconv"
	"strings"
)

// IOLimit throttles block I/O charged to the VM host cgroup on one device.
// It does not limit cached reads, tmpfs or I/O performed by unrelated services.
type IOLimit struct {
	Major               uint32 `json:"major"`
	Minor               uint32 `json:"minor"`
	ReadBytesPerSecond  uint64 `json:"read_bytes_per_second,omitempty"`
	WriteBytesPerSecond uint64 `json:"write_bytes_per_second,omitempty"`
	ReadIOPS            uint64 `json:"read_iops,omitempty"`
	WriteIOPS           uint64 `json:"write_iops,omitempty"`
}

func (v IOLimit) Device() string { return fmt.Sprintf("%d:%d", v.Major, v.Minor) }
func (v IOLimit) values() map[string]uint64 {
	return map[string]uint64{"rbps": v.ReadBytesPerSecond, "wbps": v.WriteBytesPerSecond, "riops": v.ReadIOPS, "wiops": v.WriteIOPS}
}

func ValidateAdditionalLimits(p ResourceProfile) error {
	if p.FileDescriptorLimit != 0 && (p.FileDescriptorLimit < 128 || p.FileDescriptorLimit > 1<<20) {
		return fmt.Errorf("file descriptor limit must be 0 or between 128 and 1048576")
	}
	if len(p.BlockIO) > 16 {
		return fmt.Errorf("at most 16 block I/O devices may be limited")
	}
	seen := make(map[string]bool)
	for _, v := range p.BlockIO {
		if v.Major == 0 || v.Major > 4095 || v.Minor > 1048575 || seen[v.Device()] {
			return fmt.Errorf("invalid or duplicate block I/O device")
		}
		seen[v.Device()] = true
		limited := false
		for key, n := range v.values() {
			maximum := uint64(1 << 40)
			if key == "riops" || key == "wiops" {
				maximum = 1 << 30
			}
			if n > maximum {
				return fmt.Errorf("block I/O limit exceeds supported bounds")
			}
			limited = limited || n != 0
		}
		if !limited {
			return fmt.Errorf("block I/O device requires a nonzero limit")
		}
	}
	return nil
}

func (v IOLimit) Line() string {
	parts := []string{v.Device()}
	values := v.values()
	for _, key := range []string{"rbps", "wbps", "riops", "wiops"} {
		value := "max"
		if values[key] != 0 {
			value = strconv.FormatUint(values[key], 10)
		}
		parts = append(parts, key+"="+value)
	}
	return strings.Join(parts, " ")
}

// ParseIOLimit accepts kernel-style device and key=value syntax. Omitted or
// explicit max dimensions are unlimited. Duplicate keys never use last-wins.
func ParseIOLimit(input string) (IOLimit, error) {
	var result IOLimit
	fields := strings.Fields(input)
	if len(fields) < 2 || len(fields) > 5 || strings.ContainsAny(input, "\r\n\x00") {
		return result, fmt.Errorf("use MAJOR:MINOR rbps=N wbps=N riops=N wiops=N")
	}
	major, minor, ok := strings.Cut(fields[0], ":")
	a, err := strconv.ParseUint(major, 10, 32)
	if err != nil || !ok {
		return result, fmt.Errorf("invalid block I/O device")
	}
	b, err := strconv.ParseUint(minor, 10, 32)
	if err != nil {
		return result, fmt.Errorf("invalid block I/O device")
	}
	result.Major, result.Minor = uint32(a), uint32(b)
	seen := map[string]bool{}
	for _, field := range fields[1:] {
		key, raw, ok := strings.Cut(field, "=")
		if !ok || seen[key] {
			return result, fmt.Errorf("invalid or duplicate I/O dimension")
		}
		seen[key] = true
		var n uint64
		if raw != "max" {
			n, err = strconv.ParseUint(raw, 10, 64)
			if err != nil || n == 0 {
				return result, fmt.Errorf("invalid I/O limit; use positive values or max")
			}
		}
		switch key {
		case "rbps":
			result.ReadBytesPerSecond = n
		case "wbps":
			result.WriteBytesPerSecond = n
		case "riops":
			result.ReadIOPS = n
		case "wiops":
			result.WriteIOPS = n
		default:
			return result, fmt.Errorf("unknown I/O dimension")
		}
	}
	return result, ValidateAdditionalLimits(ResourceProfile{BlockIO: []IOLimit{result}})
}

// VerifyIOLimits compares parsed dimensions, independent of kernel line/key
// ordering. Every configured dimension is checked, including unlimited ones.
func VerifyIOLimits(data string, limits []IOLimit) error {
	lines := make(map[string]map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if lines[fields[0]] != nil {
			return fmt.Errorf("duplicate observed I/O device")
		}
		values := make(map[string]string)
		for _, field := range fields[1:] {
			key, value, ok := strings.Cut(field, "=")
			if !ok || values[key] != "" {
				return fmt.Errorf("invalid observed I/O limits")
			}
			values[key] = value
		}
		lines[fields[0]] = values
	}
	for _, limit := range limits {
		for key, n := range limit.values() {
			want := "max"
			if n != 0 {
				want = strconv.FormatUint(n, 10)
			}
			if lines[limit.Device()][key] != want {
				return fmt.Errorf("host I/O limit mismatch for device %s", limit.Device())
			}
		}
	}
	return nil
}
