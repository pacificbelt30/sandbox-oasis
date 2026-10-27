package sandbox

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

//go:embed testdata/agent-state-probe.py
var agentStateProbe string

func TestKataAgentState(t *testing.T) {
	if os.Getenv("CODEX_DOCK_KATA_E2E") != "1" {
		t.Skip("requires a prepared isolated Kata node")
	}
	if os.Getenv("CODEX_DOCK_NODE_RESTART_REQUEST_DIR") == "" {
		t.Fatal("agent state fault injection requires scripts/verify-setup.py on a disposable node")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	m, err := NewManager(ManagerConfig{StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	opts := RunOptions{Name: "oaiso-state-" + randomSuffix(), Image: os.Getenv("CODEX_DOCK_KATA_TEST_IMAGE"), ProjectDir: t.TempDir(), ContainerUser: fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), NoInternet: true, Detach: true, ExecCmd: "exec sleep 600", AgentStateSize: "64MiB"}
	id, err := m.RunContext(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := m.Remove(id, true); err != nil {
			t.Error(err)
		}
	}()
	probe := func(mode string) {
		t.Helper()
		out := kataExec(t, ctx, m.cli, id, false, []string{"python3", "-c", agentStateProbe, mode, id})
		if !strings.Contains(out, "SQLITE_DURABLE_"+mode) {
			t.Fatal(out)
		}
	}
	kataExec(t, ctx, m.cli, id, false, []string{"sh", "-c", `test "$(stat -f -c %T /var/lib/oaiso-agent-state)" = ext2/ext3`})
	probe("seed")
	boot := kataExec(t, ctx, m.cli, id, false, []string{"cat", "/proc/sys/kernel/random/boot_id"})
	request := func(action string) {
		t.Helper()
		dir := os.Getenv("CODEX_DOCK_NODE_RESTART_REQUEST_DIR")
		if dir == "" {
			t.Fatal("crash/metadata tests require the disposable node controller")
		}
		if err := os.Remove(filepath.Join(dir, "complete")); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "request"), []byte(action+" "+id+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		for {
			if _, err := os.Stat(filepath.Join(dir, "complete")); err == nil {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	request("restart")
	probe("verify")
	if err := m.Stop(id, 0); err != nil {
		t.Fatal(err)
	}
	request("metadata")
	if err := m.Start(id); err != nil {
		t.Fatal(err)
	}
	probe("verify")
	if after := kataExec(t, ctx, m.cli, id, false, []string{"cat", "/proc/sys/kernel/random/boot_id"}); after == boot {
		t.Fatal("stop/start did not create a new guest")
	}
	probe("wal")
	request("crash")
	if err := m.Start(id); err != nil {
		t.Fatal("restart after abrupt VM death:", err)
	}
	probe("crash-verify")
	probe("quota")
	// A second worker gets a distinct filesystem even with the same host UID.
	opts.Name = "oaiso-state-peer-" + randomSuffix()
	peer, err := m.RunContext(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := m.Remove(peer, true); err != nil {
			t.Error(err)
		}
	}()
	kataExec(t, ctx, m.cli, peer, false, []string{"sh", "-c", `test ! -e /var/lib/oaiso-agent-state/codex/acceptance.sqlite && test "$(stat -c %a /var/lib/oaiso-agent-state)" = 700`})
	t.Log("verified: SQLite WAL, node restart, VM stop/start, missing runtime metadata regeneration, committed WAL after abrupt VM death, disk-full failure/recovery, integrity and cross-worker isolation")
}
