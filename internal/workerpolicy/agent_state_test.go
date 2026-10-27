package workerpolicy

import "testing"

func TestAgentStateCapacityBounds(t *testing.T) {
	for value, want := range map[string]int64{"": 512 << 20, "auto": 512 << 20, "off": 0, "64MiB": 64 << 20, "1GiB": 1 << 30, "8192MiB": 8 << 30, "  OFF ": 0} {
		if got, err := AgentStateBytes(value); err != nil || got != want {
			t.Fatalf("%q: %d, %v", value, got, err)
		}
	}
	for _, value := range []string{"0MiB", "63MiB", "9GiB", "8193MiB", "-64MiB", "1.5GiB", "999999999999999999999GiB", "64MB", "../state", "64MiB\nother"} {
		if _, err := AgentStateBytes(value); err == nil {
			t.Fatal("accepted", value)
		}
	}
}

func FuzzAgentStateCapacity(f *testing.F) {
	for _, value := range []string{"auto", "off", "64MiB", "8GiB", "9223372036854775807GiB"} {
		f.Add(value)
	}
	f.Fuzz(func(t *testing.T, value string) {
		size, err := AgentStateBytes(value)
		if err == nil && size != 0 && (size < 64<<20 || size > 8<<30 || size%(1<<20) != 0) {
			t.Fatal("capacity escaped its allocation bounds", size)
		}
	})
}
