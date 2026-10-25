package sandbox

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	dockernetwork "github.com/docker/docker/api/types/network"
	"github.com/pacificbelt30/codex-dock/internal/engine"
)

//go:embed testdata/workspace-sharing.py
var workspaceSharingProbe string

//go:embed testdata/workspace-watch.js
var workspacePollingProbe string

// Use the public CLI and the actual restricted entrypoint, not admin exec.
// No external requests or real credentials are needed.
func TestKataWorkspaceSharing(t *testing.T) {
	if os.Getenv("CODEX_DOCK_KATA_E2E") != "1" {
		t.Skip("set CODEX_DOCK_KATA_E2E=1 on a prepared Linux/KVM host")
	}
	binary := os.Getenv("CODEX_DOCK_KATA_TEST_BINARY")
	if binary == "" {
		t.Fatal("CODEX_DOCK_KATA_TEST_BINARY must select the CLI under test")
	}
	if os.Getuid() == 0 {
		t.Fatal("run filesystem acceptance as the non-root operator")
	}
	// Never bind credentials or alter state from the operator's real home.
	privateHome := t.TempDir()
	if err := os.Chmod(privateHome, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", privateHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(privateHome, "config"))
	t.Setenv("CODEX_DOCK_STATE_DIR", filepath.Join(privateHome, "state"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(privateHome, "xdg-state"))
	c, err := engine.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	workerImage := os.Getenv("CODEX_DOCK_KATA_TEST_IMAGE")
	if workerImage == "" {
		workerImage = "codex-dock:latest"
	}
	proxyImage := os.Getenv("CODEX_DOCK_KATA_PROXY_IMAGE")
	if proxyImage == "" {
		proxyImage = "codex-dock-proxy:latest"
	}
	prefix := "dock-sharing-" + randomSuffix()
	authName, httpName, netName := prefix+"-auth", prefix+"-http", prefix+"-egress"
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	config := filepath.Join(root, "config.toml")
	write := func(t *testing.T, path, text string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(t, config, fmt.Sprintf("proxy_image = %s\n[proxy]\nadmin_url = %s\ncontainer_url = %s\nadmin_secret = \"sharing-fixture-private-admin-at-least-32-bytes\"\nforward_default_deny = false\n", strconv.Quote(proxyImage), strconv.Quote(fmt.Sprintf("http://127.0.0.1:%d", port)), strconv.Quote("http://"+authName+":18080")), 0600)
	t.Setenv("OPENAI_API_KEY", "sk-sharing-fixture-never-valid")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	cli := func(t *testing.T, cwd string, args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, append([]string{"--config", config}, args...)...)
		cmd.Dir = cwd
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("CLI %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	t.Cleanup(func() {
		for _, name := range []string{httpName, authName} {
			if err := c.ContainerRemove(context.Background(), name, container.RemoveOptions{Force: true}); err != nil && !errdefs.IsNotFound(err) {
				t.Error("proxy cleanup:", err)
			}
		}
		if n, err := c.NetworkInspect(context.Background(), netName, dockernetwork.InspectOptions{}); err == nil {
			if err := c.NetworkRemove(context.Background(), n.ID); err != nil {
				t.Error("egress cleanup:", err)
			}
		} else if !errdefs.IsNotFound(err) {
			t.Error("egress lookup:", err)
		}
	})
	cli(t, root, "proxy", "run", "--name", authName, "--http-name", httpName, "--network", netName, "--admin-port", strconv.Itoa(port), "--admin-secret", "sharing-fixture-private-admin-at-least-32-bytes")
	checks := filepath.Join(root, "checks")
	if err := os.Mkdir(checks, 0755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(checks, "workspace-sharing.py"), workspaceSharingProbe, 0644)
	write(t, filepath.Join(checks, "workspace-watch.js"), workspacePollingProbe, 0644)

	type worker struct {
		name    string
		control string
		seq     int
	}
	readJSON := func(t *testing.T, path string, out any) bool {
		t.Helper()
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return false
		}
		if err != nil {
			t.Fatal(err)
		}
		// ready.json may be observed before the initial write finishes.
		return json.Unmarshal(data, out) == nil
	}
	launch := func(t *testing.T, cwd string, flags ...string) *worker {
		t.Helper()
		control := t.TempDir()
		if err := os.Chmod(control, 0777); err != nil {
			t.Fatal(err)
		}
		w := &worker{name: prefix + "-" + randomSuffix(), control: control}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if _, err := c.ContainerInspect(ctx, w.name); errdefs.IsNotFound(err) {
				return
			}
			cmd := exec.CommandContext(ctx, binary, "--config", config, "rm", "--force", w.name)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("worker cleanup: %v %s", err, out)
			}
		})
		args := []string{"run", "--runtime", "kata", "--name", w.name, "--image", workerImage, "--detach", "--keep", "--no-internet", "--http-proxy-url", "http://" + httpName + ":18082", "--mount", checks + ":/checks:ro", "--mount", control + ":/control:rw", "--exec", "exec python3 -u /checks/workspace-sharing.py"}
		args = append(args, flags...)
		cli(t, cwd, args...)
		var ready map[string]any
		deadline := time.Now().Add(20 * time.Second)
		for !readJSON(t, filepath.Join(control, "ready.json"), &ready) {
			if time.Now().After(deadline) {
				var diagnostic []byte
				if logs, err := c.ContainerLogs(context.Background(), w.name, container.LogsOptions{ShowStdout: true, ShowStderr: true, Tail: "30"}); err == nil {
					diagnostic, _ = io.ReadAll(io.LimitReader(logs, 8192))
					_ = logs.Close()
				}
				t.Fatalf("worker entrypoint did not start the filesystem probe: %s", diagnostic)
			}
			time.Sleep(25 * time.Millisecond)
		}
		if ready["cwd"] != "/workspace" {
			t.Fatalf("working directory: %v", ready)
		}
		t.Logf("entrypoint: uid=%v gid=%v groups=%v cwd=%v", ready["uid"], ready["gid"], ready["groups"], ready["cwd"])
		return w
	}
	guest := func(t *testing.T, w *worker, code string) map[string]any {
		t.Helper()
		w.seq++
		data, err := json.Marshal(map[string]string{"code": code})
		if err != nil {
			t.Fatal(err)
		}
		request := filepath.Join(w.control, fmt.Sprintf("request-%d.json", w.seq))
		if err := os.WriteFile(request+".tmp", data, 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(request+".tmp", request); err != nil {
			t.Fatal(err)
		}
		var result struct {
			Value map[string]any `json:"value"`
			Error string         `json:"error"`
		}
		deadline := time.Now().Add(15 * time.Second)
		for !readJSON(t, filepath.Join(w.control, fmt.Sprintf("response-%d.json", w.seq)), &result) {
			if time.Now().After(deadline) {
				t.Fatalf("filesystem response timed out: %s", code)
			}
			time.Sleep(25 * time.Millisecond)
		}
		if result.Error != "" {
			t.Fatalf("guest filesystem operation %s:\n%s", code, result.Error)
		}
		return result.Value
	}
	hostMetadata := func(t *testing.T, path string, uid, gid uint32, mode os.FileMode) {
		t.Helper()
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		s := info.Sys().(*syscall.Stat_t)
		if s.Uid != uid || s.Gid != gid || info.Mode().Perm() != mode {
			t.Fatalf("host metadata for %s: uid=%d gid=%d mode=%o; want %d:%d %o", path, s.Uid, s.Gid, info.Mode().Perm(), uid, gid, mode)
		}
	}
	hostContent := func(t *testing.T, path, expected string) {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil || string(data) != expected {
			t.Fatalf("host content for %s: %q %v; want %q", path, data, err, expected)
		}
	}

	t.Run("default-current-directory", func(t *testing.T) {
		project := filepath.Join(t.TempDir(), "project with spaces 日本語")
		if err := os.Mkdir(project, 0755); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(project, "host-private"), "host-before", 0600)
		write(t, filepath.Join(project, ".hidden"), "hidden", 0640)
		flags := []string{"--watch-polling"}
		if foreign := os.Getenv("CODEX_DOCK_KATA_OWNER_FIXTURE"); foreign != "" {
			flags = append(flags, "--mount", foreign+":/foreign:rw")
		}
		w := launch(t, project, flags...)
		if os.Getenv("CODEX_DOCK_KATA_OWNER_FIXTURE") != "" {
			guest(t, w, `import errno
for action in [lambda:Path('/foreign/foreign-private').read_text(),lambda:Path('/foreign/foreign-private').write_text('bad')]:
 try: action()
 except PermissionError: pass
 else: raise AssertionError('another UID private file was accessible')
Path('/foreign/setgid/from-current-user').write_text('setgid-inheritance')
result={}`)
			hostMetadata(t, filepath.Join(os.Getenv("CODEX_DOCK_KATA_OWNER_FIXTURE"), "setgid/from-current-user"), uint32(os.Getuid()), 12002, 0644)
			t.Log("PASS: another UID's 0600 file is denied; setgid directory preserves its group on guest-created files")
		}
		guest(t, w, `assert os.environ["CHOKIDAR_USEPOLLING"]=="true"
assert os.environ["CHOKIDAR_INTERVAL"]=="1000"
assert os.environ["WATCHPACK_POLLING"]=="1000"
assert "HTTP_PROXY" not in os.environ
result={}`)
		got := guest(t, w, `p=Path('/workspace/host-private'); s=p.stat()
assert os.getuid()==`+strconv.Itoa(os.Getuid())+` and os.getgid()==`+strconv.Itoa(os.Getgid())+`
assert (s.st_uid,s.st_gid,s.st_mode&0o777)==(os.getuid(),os.getgid(),0o600)
assert p.read_text()=='host-before'
assert Path('/workspace/.hidden').read_text()=='hidden'
p.write_text('guest-edit')
os.umask(0o027)
Path('/workspace/guest-created').write_text('guest-new')
result={'uid':os.getuid(),'gid':os.getgid()}`)
		t.Logf("verified default numeric identity: %v", got)
		hostContent(t, filepath.Join(project, "host-private"), "guest-edit")
		hostMetadata(t, filepath.Join(project, "host-private"), uint32(os.Getuid()), uint32(os.Getgid()), 0600)
		hostContent(t, filepath.Join(project, "guest-created"), "guest-new")
		hostMetadata(t, filepath.Join(project, "guest-created"), uint32(os.Getuid()), uint32(os.Getgid()), 0640)

		guest(t, w, `try: os.chown('/workspace/guest-created',12001,12002)
except PermissionError: pass
else: raise AssertionError('non-root worker could change file ownership')
result={}`)
		write(t, filepath.Join(project, "after-start"), "host-new", 0644)
		write(t, filepath.Join(project, "guest-created"), "host-edit", 0640)
		guest(t, w, `assert Path('/workspace/after-start').read_text()=='host-new'
assert Path('/workspace/guest-created').read_text()=='host-edit'
result={}`)
		write(t, filepath.Join(project, "host-private.tmp"), "host-atomic", 0600)
		if err := os.Rename(filepath.Join(project, "host-private.tmp"), filepath.Join(project, "host-private")); err != nil {
			t.Fatal(err)
		}
		guest(t, w, `assert Path('/workspace/host-private').read_text()=='host-atomic'
p=Path('/workspace/guest-created.tmp'); p.write_text('guest-atomic'); p.replace('/workspace/guest-created')
result={}`)
		hostContent(t, filepath.Join(project, "guest-created"), "guest-atomic")
		guest(t, w, `p=Path('/workspace/日本語 file'); p.write_bytes(bytes(range(256)))
p.chmod(0o751)
os.link(p, '/workspace/hardlink')
os.symlink('日本語 file','/workspace/relative-link')
assert p.stat().st_ino==Path('/workspace/hardlink').stat().st_ino
result={}`)
		data, err := os.ReadFile(filepath.Join(project, "relative-link"))
		if err != nil || len(data) != 256 || data[255] != 255 {
			t.Fatalf("binary/symlink host read: %v", err)
		}
		hostMetadata(t, filepath.Join(project, "日本語 file"), uint32(os.Getuid()), uint32(os.Getgid()), 0751)
		if err := os.Chmod(filepath.Join(project, "日本語 file"), 0700); err != nil {
			t.Fatal(err)
		}
		guest(t, w, `assert Path('/workspace/日本語 file').stat().st_mode&0o777==0o700
Path('/workspace/after-start').rename('/workspace/guest-renamed')
Path('/workspace/hardlink').unlink()
result={}`)
		if _, err := os.Stat(filepath.Join(project, "after-start")); !os.IsNotExist(err) {
			t.Fatal("guest rename not visible on host:", err)
		}
		if err := os.Rename(filepath.Join(project, "guest-renamed"), filepath.Join(project, "host-renamed")); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(project, ".hidden")); err != nil {
			t.Fatal(err)
		}
		guest(t, w, `assert Path('/workspace/host-renamed').read_text()=='host-new'
assert not Path('/workspace/guest-renamed').exists()
assert not Path('/workspace/.hidden').exists()
result={}`)
		// Test reads through an already-open descriptor as well as reopening.
		write(t, filepath.Join(project, "open-file"), "before", 0644)
		fd := int(guest(t, w, `fd=os.open('/workspace/open-file',os.O_RDONLY)
assert os.read(fd,6)==b'before'
result={'fd':fd}`)["fd"].(float64))
		write(t, filepath.Join(project, "open-file"), "after!", 0644)
		guest(t, w, fmt.Sprintf(`os.lseek(%d,0,0)
assert os.read(%d,6)==b'after!', 'host changes stayed stale in an open guest descriptor'
os.close(%d)
result={}`, fd, fd, fd))
		t.Log("PASS: host updates are visible through an already-open guest descriptor")

		// File event delivery across two kernels is measured separately.
		watch := guest(t, w, `import ctypes
libc=ctypes.CDLL(None,use_errno=True)
fd=libc.inotify_init1(os.O_NONBLOCK|os.O_CLOEXEC)
assert fd>=0
assert libc.inotify_add_watch(fd,b'/workspace',0x102)>=0
result={'fd':fd}`)
		watchFD := int(watch["fd"].(float64))
		write(t, filepath.Join(project, "host-watch-probe"), "host-event", 0644)
		events := guest(t, w, fmt.Sprintf(`import select
host_event=bool(select.select([%d],[],[],0.5)[0])
if host_event: os.read(%d,65536)
Path('/workspace/guest-watch-probe').write_text('local-event')
assert select.select([%d],[],[],1)[0], 'guest inotify positive control failed'
os.read(%d,65536)
os.close(%d)
result={'host_to_guest_inotify':host_event}`, watchFD, watchFD, watchFD, watchFD, watchFD))
		t.Logf("OBSERVATION: cross-kernel file events: %v (local guest event positive control passed)", events)
		write(t, filepath.Join(project, "polling-target"), "before-poll", 0644)
		guest(t, w, `import subprocess
process=subprocess.Popen(['node','/checks/workspace-watch.js'])
result={'pid':process.pid}`)
		for _, file := range []string{"poll-ready", "poll-observed"} {
			deadline := time.Now().Add(8 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(w.control, file)); err == nil {
					break
				} else if !os.IsNotExist(err) {
					t.Fatal(err)
				}
				if time.Now().After(deadline) {
					t.Fatalf("Node polling watcher did not publish %s", file)
				}
				time.Sleep(25 * time.Millisecond)
			}
			if file == "poll-ready" {
				write(t, filepath.Join(project, "polling-target"), "host-poll-change", 0644)
			}
		}
		hostContent(t, filepath.Join(w.control, "poll-observed"), "host-poll-change")
		t.Log("PASS: Node fs.watchFile polling observes live host edits after a guest-side positive control")

		before, err := c.ContainerInspect(context.Background(), w.name)
		if err != nil {
			t.Fatal(err)
		}
		cli(t, project, "stop", w.name)
		for _, pattern := range []string{"ready.json", "request-*", "response-*"} {
			matches, err := filepath.Glob(filepath.Join(w.control, pattern))
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range matches {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
		}
		w.seq = 0
		write(t, filepath.Join(project, "while-stopped"), "host-during-stop", 0644)
		cli(t, project, "vm", "start", w.name)
		guest(t, w, `assert Path('/workspace/while-stopped').read_text()=='host-during-stop'
Path('/workspace/after-restart').write_text('guest-after-restart')
result={}`)
		after, err := c.ContainerInspect(context.Background(), w.name)
		if err != nil || after.ID != before.ID {
			t.Fatalf("restart changed identity: %v", err)
		}
		hostContent(t, filepath.Join(project, "after-restart"), "guest-after-restart")
		hostMetadata(t, filepath.Join(project, "after-restart"), uint32(os.Getuid()), uint32(os.Getgid()), 0644)
		t.Log("PASS: same worker ID and bidirectional workspace/ownership survive stop and restart")
		t.Log("PASS: current directory, live create/edit/atomic replace/rename/delete in both directions, numeric ownership, modes, binary/unicode paths, symlinks and hardlinks")
	})

	t.Run("independent-mount-permissions", func(t *testing.T) {
		project, extra := t.TempDir(), t.TempDir()
		write(t, filepath.Join(project, "sentinel"), "original", 0600)
		write(t, filepath.Join(extra, "sentinel"), "extra", 0644)
		w := launch(t, project, "--read-only", "--mount", extra+":/extra:rw")
		guest(t, w, `import errno
assert Path('/workspace/sentinel').read_text()=='original'
for action in [lambda: Path('/workspace/new').write_text('bad'),lambda:Path('/workspace/sentinel').write_text('bad'),lambda:Path('/workspace/sentinel').unlink(),lambda:Path('/workspace/sentinel').chmod(0o777)]:
 try: action()
 except OSError as e: assert e.errno==errno.EROFS, e
 else: raise AssertionError('read-only workspace was writable')
Path('/extra/created').write_text('extra-guest')
result={}`)
		hostContent(t, filepath.Join(project, "sentinel"), "original")
		hostMetadata(t, filepath.Join(project, "sentinel"), uint32(os.Getuid()), uint32(os.Getgid()), 0600)
		hostContent(t, filepath.Join(extra, "created"), "extra-guest")
		write(t, filepath.Join(project, "sentinel"), "host-update-ro", 0600)
		guest(t, w, `assert Path('/workspace/sentinel').read_text()=='host-update-ro'
result={}`)
		t.Log("PASS: read-only workspace rejects create/edit/delete/chmod while host updates remain visible and additional rw mount stays writable")
	})

	t.Run("additional-read-only", func(t *testing.T) {
		project, extra := t.TempDir(), t.TempDir()
		write(t, filepath.Join(extra, "sentinel"), "extra", 0600)
		single := filepath.Join(t.TempDir(), "single-file")
		write(t, single, "single-file-content", 0600)
		w := launch(t, project, "--mount", extra+":/extra:ro", "--mount", single+":/single-file:ro")
		guest(t, w, `import errno
assert Path('/extra/sentinel').read_text()=='extra'
assert Path('/single-file').read_text()=='single-file-content'
try: Path('/single-file').write_text('bad')
except OSError as e: assert e.errno==errno.EROFS, e
else: raise AssertionError('read-only file mount was writable')
try: Path('/extra/new').write_text('bad')
except OSError as e: assert e.errno==errno.EROFS, e
else: raise AssertionError('read-only extra mount was writable')
Path('/workspace/created').write_text('workspace-rw')
result={}`)
		hostContent(t, filepath.Join(project, "created"), "workspace-rw")
		hostMetadata(t, filepath.Join(project, "created"), uint32(os.Getuid()), uint32(os.Getgid()), 0644)
		hostContent(t, single, "single-file-content")
		hostMetadata(t, single, uint32(os.Getuid()), uint32(os.Getgid()), 0600)
		t.Log("PASS: extra directory/file ro mounts preserve source contents/owner/mode while workspace remains writable")
	})

	t.Run("explicit-user", func(t *testing.T) {
		project := t.TempDir()
		if err := os.Chmod(project, 0777); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(project, "host-private"), "host-only", 0600)
		w := launch(t, project, "--user", "12001:12002")
		guest(t, w, `assert (os.getuid(),os.getgid())==(12001,12002)
try: Path('/workspace/host-private').read_text()
except PermissionError: pass
else: raise AssertionError('explicit UID read caller private file')
Path('/workspace/other-owner').write_text('explicit-user')
result={}`)
		hostMetadata(t, filepath.Join(project, "other-owner"), 12001, 12002, 0644)
		t.Log("PASS: explicit UID/GID are preserved on the host without ownership translation")
	})
	t.Run("relative-project-and-extra-mount", func(t *testing.T) {
		base := t.TempDir()
		project, extra := filepath.Join(base, "real-project"), filepath.Join(base, "data")
		for _, path := range []string{project, extra} {
			if err := os.Mkdir(path, 0755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Symlink("real-project", filepath.Join(base, "project-link")); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(project, "sentinel"), "selected-project", 0644)
		write(t, filepath.Join(extra, "sentinel"), "relative-extra", 0644)
		w := launch(t, base, "--project", "./project-link", "--mount", "./data:/extra:rw")
		guest(t, w, `assert Path('/workspace/sentinel').read_text()=='selected-project'
assert Path('/extra/sentinel').read_text()=='relative-extra'
Path('/extra/created').write_text('relative-guest')
result={}`)
		hostContent(t, filepath.Join(extra, "created"), "relative-guest")
		hostMetadata(t, filepath.Join(extra, "created"), uint32(os.Getuid()), uint32(os.Getgid()), 0644)
		t.Log("PASS: relative --project, symlink project source, and relative additional mount")
	})
	t.Run("directory-owner", func(t *testing.T) {
		project := os.Getenv("CODEX_DOCK_KATA_OWNER_FIXTURE")
		if project == "" {
			project = t.TempDir()
		}
		info, err := os.Stat(project)
		if err != nil {
			t.Fatal(err)
		}
		owner := info.Sys().(*syscall.Stat_t)
		w := launch(t, project, "--user", "dir")
		guest(t, w, fmt.Sprintf(`assert (os.getuid(),os.getgid())==(%d,%d)
Path('/workspace/from-dir-owner').write_text('directory-owner')
result={}`, owner.Uid, owner.Gid))
		hostMetadata(t, filepath.Join(project, "from-dir-owner"), owner.Uid, owner.Gid, 0644)
		t.Logf("PASS: --user dir follows directory owner %d:%d (caller is %d:%d)", owner.Uid, owner.Gid, os.Getuid(), os.Getgid())
	})
	t.Run("worktree", func(t *testing.T) {
		repo, _ := gitFixture(t)
		write(t, filepath.Join(repo, "original-only"), "original", 0644)
		w := launch(t, repo, "--worktree", "--branch", prefix+"-branch", "--new-branch")
		info, err := c.ContainerInspect(context.Background(), w.name)
		if err != nil {
			t.Fatal(err)
		}
		var workspace string
		for _, bind := range info.HostConfig.Mounts {
			if bind.Target == "/workspace" {
				workspace = bind.Source
			}
		}
		if workspace == "" || workspace == repo {
			t.Fatal("worktree did not select a separate workspace")
		}
		pointer, err := os.ReadFile(filepath.Join(workspace, ".git"))
		if err != nil {
			t.Fatal(err)
		}
		guest(t, w, `import subprocess
assert not Path('/workspace/original-only').exists()
Path('/workspace/worktree-change').write_text('guest-worktree')
subprocess.run(['git','add','worktree-change'],check=True)
subprocess.run(['git','-c','user.name=Fixture','-c','user.email=fixture@example.invalid','-c','commit.gpgsign=false','commit','-m','sharing-check'],check=True)
result={}`)
		hostContent(t, filepath.Join(workspace, "worktree-change"), "guest-worktree")
		hostMetadata(t, filepath.Join(workspace, "worktree-change"), uint32(os.Getuid()), uint32(os.Getgid()), 0644)
		if _, err := os.Stat(filepath.Join(repo, "worktree-change")); !os.IsNotExist(err) {
			t.Fatal("worktree change leaked into original checkout")
		}
		after, err := os.ReadFile(filepath.Join(workspace, ".git"))
		if err != nil || string(after) != string(pointer) {
			t.Fatal("Git pointer changed")
		}
		cli(t, repo, "rm", "--force", w.name)
		if _, err := os.Stat(workspace); !os.IsNotExist(err) {
			t.Fatal("managed worktree not reclaimed:", err)
		}
		hostContent(t, filepath.Join(repo, "original-only"), "original")
		t.Log("PASS: worktree uses a separate checkout, preserves ownership and Git commit, and removes only the managed checkout")
	})

	t.Run("default-egress-denied", func(t *testing.T) {
		original, err := os.ReadFile(config)
		if err != nil {
			t.Fatal(err)
		}
		write(t, config, strings.Replace(string(original), "forward_default_deny = false\n", "", 1), 0600)
		t.Cleanup(func() { write(t, config, string(original), 0600) })
		w := launch(t, t.TempDir(), "--no-internet=false")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		inspect, err := c.ContainerInspect(ctx, w.name)
		if err != nil || inspect.Config == nil {
			t.Fatalf("inspect default worker: %v", err)
		}
		authEndpoint, ok := envValue(inspect.Config.Env, "CODEX_AUTH_PROXY_URL")
		if !ok || authEndpoint == "" {
			t.Fatal("default worker has no pinned auth endpoint")
		}
		guest(t, w, `import socket,urllib.request
assert not os.environ.get('HTTP_PROXY') and not os.environ.get('HTTPS_PROXY')
assert not any(line.split()[1]=='00000000' for line in Path('/proc/net/route').read_text().splitlines()[1:])
try:
    socket.create_connection(('203.0.113.1',443),timeout=1)
except OSError:
    pass
else:
    raise AssertionError('direct external connection succeeded')
opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
with opener.open(`+strconv.Quote(authEndpoint+"/health")+`,timeout=5) as response:
    assert response.status==200
result={}`)
		var policy struct {
			GeneralEgress bool `json:"general_egress"`
		}
		if err := json.Unmarshal([]byte(cli(t, w.control, "policy", "show", "--worker", w.name, "--json")), &policy); err != nil || policy.GeneralEgress {
			t.Fatalf("default worker has general egress: %+v %v", policy, err)
		}
		t.Log("PASS: omitted policy disables general egress and direct external access while keeping auth reachable")
	})

	t.Run("effective-policy-with-pinned-IP", func(t *testing.T) {
		project := t.TempDir()
		w := launch(t, project, "--no-internet=false")
		output := cli(t, project, "policy", "show", "--worker", w.name, "--json")
		var policy struct {
			GeneralEgress bool   `json:"general_egress"`
			PolicyID      string `json:"policy_id"`
			Proxy         string `json:"proxy"`
		}
		if err := json.Unmarshal([]byte(output), &policy); err != nil {
			t.Fatalf("policy JSON: %v %s", err, output)
		}
		if !policy.GeneralEgress || policy.PolicyID == "" || policy.Proxy == "" {
			t.Fatalf("missing effective policy: %+v", policy)
		}
		t.Log("PASS: effective policy resolves the actual immutable proxy from the Kata worker's pinned IP")
	})

}
