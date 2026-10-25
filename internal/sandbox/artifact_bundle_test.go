package sandbox

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestArtifactBundleRequiresReviewedContentRejectsSecretsAndSymlinks(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	outside := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	git("init")
	git("config", "user.name", "Fixture")
	git("config", "user.email", "fixture@example.invalid")
	if err = os.WriteFile(filepath.Join(root, "tracked"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "tracked")
	git("-c", "commit.gpgsign=false", "commit", "-m", "fixture")
	_ = os.WriteFile(filepath.Join(root, "tracked"), []byte("new"), 0600)
	_ = os.WriteFile(filepath.Join(root, "untracked"), []byte("result"), 0600)
	_ = os.WriteFile(filepath.Join(root, "json.py"), []byte("raise RuntimeError('workspace import must not execute')"), 0600)
	_ = os.WriteFile(filepath.Join(root, ".env"), []byte("excluded-secret"), 0600)
	_ = os.WriteFile(filepath.Join(outside, "secret"), []byte("outside-secret"), 0600)
	if err = os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(root)
	program := strings.ReplaceAll(artifactProgram, "'/workspace'", string(encoded))
	run := func(op string, o ArtifactOptions) ([]byte, error) {
		raw, _ := json.Marshal(o)
		cmd := exec.CommandContext(context.Background(), python, "-I", "-c", program, op, "worker", string(raw))
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		return cmd.Output()
	}
	options := ArtifactOptions{Paths: []string{"tracked", "untracked"}}
	out, err := run("plan", options)
	if err != nil {
		t.Fatal(err)
	}
	var plan ArtifactPlan
	if err = json.Unmarshal(out, &plan); err != nil {
		t.Fatal(err)
	}
	options.ExpectedPlan = plan.ID
	bundle, err := run("bundle", options)
	if err != nil {
		t.Fatal(err)
	}
	tarReader := tar.NewReader(bytes.NewReader(bundle))
	entries := map[string][]byte{}
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(tarReader)
		if err != nil {
			t.Fatal(err)
		}
		entries[header.Name] = data
	}
	if len(entries) != 4 || string(entries["files/untracked"]) != "result" || !bytes.Contains(entries["changes.patch"], []byte("+new")) {
		t.Fatal("missing artifact contents")
	}
	if bytes.Contains(bundle, []byte("excluded-secret")) || bytes.Contains(bundle, []byte("outside-secret")) {
		t.Fatal("artifact leaked excluded files")
	}
	_ = os.WriteFile(filepath.Join(root, "untracked"), []byte("changed"), 0600)
	if _, err = run("bundle", options); err == nil {
		t.Fatal("changed plan exported")
	}
	for _, path := range []string{".env", "link", "../secret", ".git/config", "/etc/passwd"} {
		if _, err = run("plan", ArtifactOptions{Paths: []string{path}}); err == nil {
			t.Fatal("unsafe artifact", path)
		}
	}
	if _, err = run("plan", ArtifactOptions{Paths: []string{"tracked"}, MaxBytes: 1}); err == nil {
		t.Fatal("oversized artifact accepted")
	}
}
