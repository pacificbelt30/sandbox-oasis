package sandbox_test

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/sandbox"
)

func TestKataQualityRoundTwo(t *testing.T) {
	if os.Getenv("CODEX_DOCK_KATA_E2E") != "1" {
		t.Skip("requires disposable matching Kata node")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	binary := os.Getenv("CODEX_DOCK_KATA_TEST_BINARY")
	if binary == "" {
		t.Fatal("matching CLI binary required")
	}
	project := t.TempDir()
	script := filepath.Join(project, "script.sh")
	const originalScript = "#!/bin/sh\nprintf 'ORIGINAL\\n'\n"
	if err := os.WriteFile(script, []byte(originalScript), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(script, filepath.Join(project, "original-alias")); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"add", "script.sh"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false", "commit", "-m", "initial"}} {
		command := exec.CommandContext(ctx, "git", args...)
		command.Dir = project
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	manager, err := sandbox.NewManager(sandbox.ManagerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	id, err := manager.RunContext(ctx, sandbox.RunOptions{Name: fmt.Sprintf("quality2-%d", time.Now().UnixNano()), Image: os.Getenv("CODEX_DOCK_KATA_TEST_IMAGE"), ProjectDir: project, ContainerUser: fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), ResourceProfile: "compact", NoInternet: true, NoGitMetadata: true, Detach: true, ExecCmd: "exec sleep 300", AgentStateSize: "off"})
	if id != "" {
		defer func() {
			cleanup, stop := context.WithTimeout(context.Background(), time.Minute)
			defer stop()
			if err := manager.RemoveContext(cleanup, id, true); err != nil {
				t.Error(err)
			}
		}()
	}
	if err != nil {
		t.Fatal(err)
	}
	cli := func(input string, args ...string) ([]byte, error) {
		command := exec.CommandContext(ctx, binary, args...)
		command.Dir = project
		command.Stdin = strings.NewReader(input)
		return command.CombinedOutput()
	}
	apiText := "#!/bin/sh\nprintf 'API_MODE_OK\\n'\n"
	if err := manager.WriteFile(ctx, id, "script.sh", strings.NewReader(apiText)); err != nil {
		t.Fatal(err)
	}
	check := func(want string) {
		t.Helper()
		st, err := os.Stat(script)
		if err != nil || st.Mode().Perm() != 0755 {
			t.Fatal("executable mode lost", st, err)
		}
		var out bytes.Buffer
		code, err := manager.Exec(ctx, id, []string{"/workspace/script.sh"}, nil, &out, &out)
		if err != nil || code != 0 || out.String() != want {
			t.Fatalf("updated executable: code=%d err=%v out=%q", code, err, out.String())
		}
	}
	check("API_MODE_OK\n")
	cliText := "#!/bin/sh\nprintf 'CLI_MODE_OK\\n'\n"
	if out, err := cli(cliText, "files", "put", id, "script.sh"); err != nil {
		t.Fatalf("CLI upload %v %s", err, out)
	}
	check("CLI_MODE_OK\n")
	alias, err := os.ReadFile(filepath.Join(project, "original-alias"))
	if err != nil || string(alias) != originalScript {
		t.Fatal("atomic replacement changed original alias", err)
	}
	destination := filepath.Join(t.TempDir(), "download")
	if out, err := cli("", "files", "get", id, "script.sh", "--local", destination); err != nil {
		t.Fatalf("CLI download %v %s", err, out)
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != cliText {
		t.Fatal("download mismatch", err)
	}
	if out, err := cli("", "files", "get", id, "script.sh", "--local", destination); err == nil || !bytes.Contains(out, []byte("new host file")) {
		t.Fatal("existing download destination not rejected", err, string(out))
	}
	raw, err := cli("", "artifact", "plan", id, "--include", "script.sh")
	if err != nil {
		t.Fatalf("artifact plan %v %s", err, raw)
	}
	var plan sandbox.ArtifactPlan
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err, string(raw))
	}
	archive := filepath.Join(t.TempDir(), "reviewed.tar")
	if out, err := cli("", "artifact", "bundle", id, "--include", "script.sh", "--plan-id", plan.ID, "--output", archive); err != nil {
		t.Fatalf("artifact bundle %v %s", err, out)
	}
	backup, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	st, err := backup.Stat()
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("artifact not private", err)
	}
	reader := tar.NewReader(backup)
	found := false
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Name == "files/script.sh" {
			data, err := io.ReadAll(reader)
			if err != nil || string(data) != cliText {
				t.Fatal("artifact bytes changed", err)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("reviewed file missing from archive")
	}
	before := filepath.Join(t.TempDir(), "before.json")
	if out, err := cli("", "ps", "-a", "--filter", "id:"+id, "--format", "json", "--save-snapshot", before); err != nil {
		t.Fatalf("snapshot %v %s", err, out)
	}
	if err := manager.StopContext(ctx, id, 5); err != nil {
		t.Fatal(err)
	}
	raw, err = cli("", "ps", "-a", "--filter", "id:"+id, "--format", "csv", "--compare-snapshot", before)
	if err != nil {
		t.Fatalf("CSV diff %v %s", err, raw)
	}
	rows, err := csv.NewReader(bytes.NewReader(raw)).ReadAll()
	if err != nil || len(rows) < 2 || strings.Join(rows[0], ",") != "change,id,name,field,before,after" {
		t.Fatal("CSV diff is not structured", rows, err)
	}
	stopped := false
	for _, row := range rows[1:] {
		if row[0] == "stopped" && row[1] == id && row[3] == "status" && row[5] == "exited" {
			stopped = true
		}
	}
	if !stopped {
		t.Fatal("stop transition missing from CSV", rows)
	}
	if err := manager.StartContext(ctx, id); err != nil {
		t.Fatal(err)
	}
	check("CLI_MODE_OK\n")
	for _, args := range [][]string{{"proxy", "stop", "unintended-target"}, {"proxy", "rm", "unintended-target"}} {
		if out, err := cli("", args...); err == nil || !bytes.Contains(out, []byte("unknown command")) && !bytes.Contains(out, []byte("accepts 0 arg")) {
			t.Fatal("unexpected global target ignored", err, string(out))
		}
	}
	policy := filepath.Join(t.TempDir(), "policy.toml")
	const marker = "retain policy draft\n"
	if err := os.WriteFile(policy, []byte(marker), 0600); err != nil {
		t.Fatal(err)
	}
	if out, err := cli("", "--agent-policy-file", policy, "agent-policy", "init", "--force", "unintended-target"); err == nil {
		t.Fatal("unexpected init argument accepted", string(out))
	}
	data, err = os.ReadFile(policy)
	if err != nil || string(data) != marker {
		t.Fatal("invalid policy command changed saved data", err)
	}
	t.Log("verified: API/CLI executable replacement, original hardlink retention, private/no-clobber downloads, reviewed CLI artifact bundle, CSV stop comparison, restart, rejection of accidental global targets and retained policy")
}
