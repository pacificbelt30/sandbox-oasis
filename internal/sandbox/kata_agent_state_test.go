package sandbox

import (
	"context"
	_ "embed"
	"fmt"
	"github.com/pacificbelt30/codex-dock/internal/engine"
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
	backup := filepath.Join(t.TempDir(), "agent-state.backup")
	if err := engine.ExportAgentState(ctx, engine.Socket(), id, backup); err == nil {
		t.Fatal("live disk backup accepted")
	}
	usage, err := m.RootFSUsage(ctx, id)
	if err != nil || !usage.Available || usage.LimitBytes != 4<<30 || usage.LimitInodes != 65536 {
		t.Fatal("host-observed quota unavailable", usage, err)
	}
	storage, err := engine.StorageStatus(ctx, engine.Socket())
	if err != nil || !storage.Enforced || storage.AvailableBytes == 0 {
		t.Fatal("dedicated quota diagnostics", storage, err)
	}
	kataExec(t, ctx, m.cli, id, false, []string{"python3", "-c", `import os,pathlib,subprocess,sys,time
ready=pathlib.Path('/workspace/backup-wal-ready');ready.unlink(missing_ok=True)
child="""import os,pathlib,sqlite3,time
root=pathlib.Path(os.environ['CODEX_SQLITE_HOME'])
db=sqlite3.connect(root/'portable.sqlite')
assert db.execute('PRAGMA journal_mode=WAL').fetchone()[0]=='wal'
db.execute('PRAGMA synchronous=FULL');db.execute('PRAGMA wal_autocheckpoint=0')
db.execute('CREATE TABLE retained(value TEXT)');db.execute("INSERT INTO retained VALUES ('committed-in-WAL')");db.commit()
assert (root/'portable.sqlite-wal').stat().st_size>0
pathlib.Path('/workspace/backup-wal-ready').write_text('committed')
time.sleep(600)
"""
subprocess.Popen([sys.executable,'-c',child],stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,start_new_session=True)
deadline=time.monotonic()+10
while not ready.exists():
 assert time.monotonic()<deadline,'WAL commit not ready'
 time.sleep(.05)
`})
	request("crash")
	// Remove only the dead task; do not boot/replay the source filesystem before
	// backing up its committed WAL and ext4 journal.
	if err = m.Stop(id, 0); err != nil {
		t.Fatal(err)
	}
	if err = engine.ExportAgentState(ctx, engine.Socket(), id, backup); err != nil {
		t.Fatal("offline backup", err)
	}
	if err = engine.RestoreAgentState(ctx, engine.Socket(), id, backup); err == nil {
		t.Fatal("restore overwrote a used source")
	}
	targetOpts := opts
	targetOpts.Name = "oaiso-state-restore-" + randomSuffix()
	targetOpts.CreateOnly = true
	target, err := m.RunContext(ctx, targetOpts)
	if err != nil {
		t.Fatal(err)
	}
	targetRemoved := false
	defer func() {
		if !targetRemoved {
			if err := m.Remove(target, true); err != nil {
				t.Error(err)
			}
		}
	}()
	// Reject a corrupt file without altering the new destination's empty disk.
	damaged := filepath.Join(t.TempDir(), "damaged.backup")
	data, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	data[len(data)-1] ^= 1
	if err = os.WriteFile(damaged, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = engine.RestoreAgentState(ctx, engine.Socket(), target, damaged); err == nil {
		t.Fatal("corrupt backup accepted")
	}
	if err = engine.RestoreAgentState(ctx, engine.Socket(), target, backup); err != nil {
		t.Fatal("offline restore", err)
	}
	if err = engine.RestoreAgentState(ctx, engine.Socket(), target, backup); err == nil {
		t.Fatal("already-restored disk overwritten")
	}
	if err = m.Start(target); err != nil {
		t.Fatal("restored disk boot", err)
	}
	out := kataExec(t, ctx, m.cli, target, false, []string{"python3", "-c", agentStateProbe, "crash-verify", id})
	if !strings.Contains(out, "SQLITE_DURABLE_crash-verify") {
		t.Fatal(out)
	}
	kataExec(t, ctx, m.cli, target, false, []string{"python3", "-c", `import os,pathlib,sqlite3
root=pathlib.Path(os.environ['CODEX_SQLITE_HOME'])
with sqlite3.connect(root/'portable.sqlite') as db:
 assert db.execute('SELECT value FROM retained').fetchall()==[('committed-in-WAL',)]
 assert db.execute('PRAGMA integrity_check').fetchone()[0]=='ok'
print('OFFLINE_WAL_RESTORE_PASS')`})
	if err = engine.RestoreAgentState(ctx, engine.Socket(), target, backup); err == nil {
		t.Fatal("live restore accepted")
	}
	if err = m.Remove(target, true); err != nil {
		t.Fatal(err)
	}
	targetRemoved = true
	t.Log("verified: offline private disk export/restore, corruption/live/used-target rejection, committed SQLite WAL in a new VM, host quota diagnostics")
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
