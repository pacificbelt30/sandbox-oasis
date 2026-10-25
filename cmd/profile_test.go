package cmd

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/pacificbelt30/codex-dock/internal/kataguard"
)

func TestProfileJSONMatchesKataLimits(t *testing.T) {
	cmd := newProfileCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got []kataguard.ResourceProfile
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Name != "standard" || got[0].GuestMemoryBytes != kataguard.GuestMemory || got[0].HostMemoryBytes != kataguard.HostMemory || got[1].Name != "small" || got[1].GuestMemoryBytes != kataguard.SmallGuestMemory || got[2].Name != "compact" || got[2].GuestMemoryBytes != kataguard.CompactGuestMemory || got[2].HostMemoryBytes != kataguard.CompactHostMemory {
		t.Fatalf("profile = %+v", got)
	}
}

func TestProfileDefinitionIsValidNodeInput(t *testing.T) {
	command := newProfileCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"define", "compact-256", "--pids-limit", "256"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	var profiles []kataguard.ResourceProfile
	if err := json.Unmarshal(output.Bytes(), &profiles); err != nil || len(profiles) != 1 {
		t.Fatalf("invalid node JSON: %s %v", output.Bytes(), err)
	}
	profile := profiles[0]
	if profile.Name != "compact-256" || profile.PIDLimit != 256 || profile.GuestMemoryBytes != kataguard.CompactGuestMemory || profile.HostMemoryBytes != kataguard.CompactHostMemory {
		t.Fatalf("unexpected profile: %+v", profile)
	}
	if err := kataguard.ValidateCustomResourceProfile(profile); err != nil {
		t.Fatal(err)
	}
}

func TestProfileDefinitionRejectsUnboundedAndReservedInputs(t *testing.T) {
	for _, args := range [][]string{
		{"define", "task"},
		{"define", "task", "--pids-limit", "0"},
		{"define", "task", "--pids-limit", "63"},
		{"define", "task", "--pids-limit", "4097"},
		{"define", "compact", "--pids-limit", "256"},
		{"define", "task", "--base", "unknown", "--pids-limit", "256"},
	} {
		command := newProfileCommand()
		var output bytes.Buffer
		command.SetOut(&output)
		command.SetErr(&bytes.Buffer{})
		command.SilenceUsage, command.SilenceErrors = true, true
		command.SetArgs(args)
		if err := command.Execute(); err == nil || output.Len() != 0 {
			t.Fatalf("invalid definition accepted: args=%v output=%s error=%v", args, output.Bytes(), err)
		}
	}
}

func TestProfileDefinitionIncludesFDAndBlockIO(t *testing.T) {
	command := newProfileCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"define", "limited", "--pids-limit", "256", "--nofile", "512", "--io-limit", "8:0 wbps=1048576 wiops=100"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	var profiles []kataguard.ResourceProfile
	if err := json.Unmarshal(output.Bytes(), &profiles); err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || profiles[0].FileDescriptorLimit != 512 || len(profiles[0].BlockIO) != 1 || profiles[0].BlockIO[0].WriteIOPS != 100 {
		t.Fatalf("lost limits: %s", output.Bytes())
	}
}
