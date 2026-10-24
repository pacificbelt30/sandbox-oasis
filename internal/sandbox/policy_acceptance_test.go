package sandbox

import (
	"bytes"
	"context"
	"errors"
	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/workerpolicy"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Run separately on the prepared, matching node. Uses no real credentials and
// touches only its own worker/network/workspace.
func TestKataPolicyFeatures(t *testing.T) {
	if os.Getenv("CODEX_DOCK_KATA_POLICY_E2E") != "1" {
		t.Skip("set CODEX_DOCK_KATA_POLICY_E2E=1 on an isolated matching node")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	project := t.TempDir()
	for name, value := range map[string]string{"secret": "secret-must-be-masked", "readonly": "unchanged", "normal": "normal-content"} {
		if err := os.WriteFile(filepath.Join(project, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	image := os.Getenv("CODEX_DOCK_KATA_TEST_IMAGE")
	if image == "" {
		image = "codex-dock:latest"
	}
	m, err := NewManager(ManagerConfig{Socket: engine.Socket()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	opts := RunOptions{ProjectDir: project, Image: image, Runtime: RuntimeKata, Name: "cxd-policy-test-" + randomSuffix(), ContainerUser: strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()), Detach: true, NoInternet: true, ExecCmd: "exec sleep infinity", ReadOnlyRootFS: true, Filesystem: workerpolicy.Filesystem{DenyRead: []string{"/workspace/secret"}, DenyWrite: []string{"/workspace/readonly"}}, Environment: workerpolicy.Environment{StartHooks: [][]string{{"sh", "-c", "printf hook-ready > /workspace/hook"}}, Readiness: []string{"test", "-f", "/workspace/hook"}, ReadinessTimeout: 30 * time.Second}}
	opts.Lifecycle = workerpolicy.Lifecycle{ProtectMain: true, IdleTimeout: 200 * time.Millisecond, MaxLifetime: time.Hour}
	id, err := m.RunContext(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := m.RemoveContext(context.Background(), id, true); err != nil {
			t.Error(err)
		}
	}()
	run := func(argv ...string) (int64, string) {
		t.Helper()
		var out, stderr bytes.Buffer
		code, err := m.Exec(ctx, id, argv, nil, &out, &stderr)
		if err != nil {
			t.Fatalf("exec %q: %v %s", argv, err, stderr.String())
		}
		return code, out.String()
	}
	if _, data := run("cat", "/workspace/secret"); strings.Contains(data, "secret-must-be-masked") {
		t.Fatal("masked file was readable")
	}
	if code, _ := run("sh", "-c", "printf changed > /workspace/readonly"); code == 0 {
		t.Fatal("readonly path writable")
	}
	if code, flags := run("python3", "-c", "import os; print(bool(os.statvfs('/').f_flag & os.ST_RDONLY))"); code != 0 || strings.TrimSpace(flags) != "True" {
		t.Fatalf("root filesystem is not mounted read-only: %q", flags)
	}
	var normal bytes.Buffer
	if err = m.ReadFile(ctx, id, "normal", &normal); err != nil || normal.String() != "normal-content" {
		t.Fatalf("read: %q %v", normal.String(), err)
	}
	if err = m.WriteFile(ctx, id, "uploaded", strings.NewReader("upload-content")); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(project, "uploaded")); err != nil || string(data) != "upload-content" {
		t.Fatalf("upload %s %v", data, err)
	}
	if err = os.Symlink("/etc/passwd", filepath.Join(project, "escape")); err != nil {
		t.Fatal(err)
	}
	if err = m.ReadFile(ctx, id, "escape", &normal); err == nil {
		t.Fatal("symlink followed")
	}
	var env bytes.Buffer
	code, err := m.ExecWithOptions(ctx, id, ExecOptions{Argv: []string{"sh", "-c", "printf '%s:' \"$CXD_TEST\"; pwd"}, Env: []string{"CXD_TEST=present"}, WorkingDir: "/tmp", Stdout: &env})
	if err != nil || code != 0 || env.String() != "present:/tmp\n" {
		t.Fatalf("exec options %q %d %v", env.String(), code, err)
	}
	cancelCtx, cancelExec := context.WithCancel(ctx)
	defer cancelExec()
	// Keep attached stdin open until cancellation. Closing it before Kata's
	// passfd peer opens races process startup and tests EOF instead of cancel.
	cancelInput, cancelWriter := io.Pipe()
	defer cancelInput.Close()
	defer cancelWriter.Close()
	var canceledID string
	var canceledOutput bytes.Buffer
	code, err = m.ExecWithOptions(cancelCtx, id, ExecOptions{Argv: []string{"sleep", "60"}, Stdin: cancelInput, Stdout: &canceledOutput, Stderr: &canceledOutput, OnStart: func(eid string) { canceledID = eid; go func() { time.Sleep(300 * time.Millisecond); cancelExec() }() }})
	if !errors.Is(err, context.Canceled) || canceledID == "" {
		t.Fatalf("exec cancellation: id=%s exit=%d err=%v output=%s", canceledID, code, err, canceledOutput.String())
	}
	for i := 0; i < 50; i++ {
		state, err := m.cli.ContainerExecInspect(ctx, canceledID)
		if err != nil {
			t.Fatal(err)
		}
		if !state.Running {
			break
		}
		time.Sleep(100 * time.Millisecond)
		if i == 49 {
			t.Fatal("canceled stdin execution survived")
		}
	}
	input, writer := io.Pipe()
	var terminal bytes.Buffer
	_, err = m.ExecWithOptions(ctx, id, ExecOptions{Argv: []string{"sh", "-c", "read value; stty size"}, TTY: true, Stdin: input, Stdout: &terminal, OnStart: func(eid string) {
		go func() {
			defer func() { _ = writer.Close() }()
			for i := 0; i < 50; i++ {
				if m.ResizeSession(ctx, id, eid, 111, 44) == nil {
					_, _ = io.WriteString(writer, "ready\n")
					return
				}
				time.Sleep(100 * time.Millisecond)
			}
		}()
	}})
	_ = input.Close()
	if err != nil || !strings.Contains(terminal.String(), "44 111") {
		t.Fatalf("PTY resize %q %v", terminal.String(), err)
	}
	job, err := m.StartJob(ctx, id, ExecOptions{Argv: []string{"sh", "-c", "printf job-start; printf job-start > /workspace/job-marker; sleep 5; printf job-end; printf job-end >> /workspace/job-marker"}})
	if err != nil {
		t.Fatal(err)
	}
	if requestDir := os.Getenv("CODEX_DOCK_NODE_RESTART_REQUEST_DIR"); requestDir != "" {
		_ = os.Remove(filepath.Join(requestDir, "complete"))
		if err = os.WriteFile(filepath.Join(requestDir, "request"), []byte("restart\n"), 0600); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 600; i++ {
			if _, err := os.Stat(filepath.Join(requestDir, "complete")); err == nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
			if i == 599 {
				t.Fatal("job recovery restart timed out")
			}
		}
	}
	for i := 0; i < 100; i++ {
		state, err := m.Job(ctx, id, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !state.Running {
			if state.ExitCode != 0 {
				t.Fatalf("job exit %d", state.ExitCode)
			}
			break
		}
		time.Sleep(100 * time.Millisecond)
		if i == 99 {
			t.Fatal("job did not complete")
		}
	}
	logs, err := m.JobLogs(ctx, id, job.ID, 0)
	if err != nil || logs.Output != "job-startjob-end" {
		marker, markerErr := os.ReadFile(filepath.Join(project, "job-marker"))
		t.Fatalf("job logs %+v %v marker=%q markerErr=%v", logs, err, marker, markerErr)
	}
	if jobs, err := m.ListJobs(ctx, id); err != nil || len(jobs) != 1 {
		t.Fatalf("jobs %+v %v", jobs, err)
	}
	time.Sleep(300 * time.Millisecond)
	if actions, err := m.Reap(ctx, time.Now(), false); err != nil || len(actions) != 0 {
		t.Fatalf("idle collection touched a live primary job: %+v %v", actions, err)
	}
	heartbeatOpts := opts
	heartbeatOpts.Name += "-heartbeat"
	heartbeatOpts.Lifecycle = workerpolicy.Lifecycle{IdleTimeout: 200 * time.Millisecond}
	heartbeatID, err := m.RunContext(ctx, heartbeatOpts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := m.RemoveContext(context.Background(), heartbeatID, true); err != nil {
			t.Error(err)
		}
	}()
	if _, err := m.Heartbeat(ctx, heartbeatID, time.Minute); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if actions, err := m.Reap(ctx, time.Now(), false); err != nil || len(actions) != 0 {
		t.Fatalf("live lease was reaped: %+v %v", actions, err)
	}
	if _, err := m.Heartbeat(ctx, heartbeatID, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if actions, err := m.Reap(ctx, time.Now(), false); err != nil || len(actions) != 1 || actions[0].Worker != heartbeatID || actions[0].Reason != "idle_timeout" {
		t.Fatalf("released lease did not return to idle policy: %+v %v", actions, err)
	}
	if actions, err := m.Reap(ctx, time.Now().Add(2*time.Hour), false); err != nil || len(actions) != 1 || actions[0].Worker != id || actions[0].Reason != "max_lifetime" {
		t.Fatalf("main protection extended maximum lifetime: %+v %v", actions, err)
	}
	t.Log("PASS: OCI masks/readonly/rootfs, readiness, safe files, exec options/cancellation, PTY, jobs, primary idle protection, activity leases and independent maximum lifetime in real Kata")
}
