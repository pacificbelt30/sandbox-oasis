package sandbox

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestArtifactLaunchComparisonIncludesCommittedChangesAndInvalidatesPlan(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatal(err, string(out))
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("config", "user.name", "fixture")
	git("config", "user.email", "fixture@example.invalid")
	file := filepath.Join(dir, "tracked.txt")
	if err := os.WriteFile(file, []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "tracked.txt")
	git("-c", "commit.gpgsign=false", "commit", "-qm", "base")
	base := git("rev-parse", "HEAD")
	if err := os.WriteFile(file, []byte("committed after launch\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("-c", "commit.gpgsign=false", "commit", "-qam", "guest")
	encoded, _ := json.Marshal(dir)
	program := strings.ReplaceAll(artifactProgram, "'/workspace'", string(encoded))
	run := func(op string, options map[string]any) ([]byte, error) {
		raw, _ := json.Marshal(options)
		cmd := exec.Command("python3", "-I", "-c", program, op, "worker", string(raw))
		cmd.Dir = dir
		return cmd.Output()
	}
	options := map[string]any{"paths": []string{"tracked.txt"}}
	output, err := run("plan", options)
	if err != nil {
		t.Fatal(err)
	}
	var headPlan ArtifactPlan
	if err = json.Unmarshal(output, &headPlan); err != nil {
		t.Fatal(err)
	}
	if headPlan.PatchBytes != 0 {
		t.Fatal("default HEAD behavior changed", headPlan)
	}
	options["since_launch"] = true
	options["_base"] = base
	output, err = run("plan", options)
	if err != nil {
		t.Fatal(err)
	}
	var launchPlan ArtifactPlan
	if err = json.Unmarshal(output, &launchPlan); err != nil {
		t.Fatal(err)
	}
	if launchPlan.Base != base || launchPlan.PatchBytes == 0 || launchPlan.ID == headPlan.ID {
		t.Fatal("launch patch missing", launchPlan)
	}
	options["expected_plan"] = launchPlan.ID
	archive, err := run("bundle", options)
	if err != nil || !bytes.Contains(archive, []byte("committed after launch")) {
		t.Fatal("reviewed bundle", err)
	}
	if err := os.WriteFile(file, []byte("changed after review\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := run("bundle", options); err == nil {
		t.Fatal("stale launch plan exported")
	}
	options["_base"] = "--output=escape"
	if _, err := run("plan", options); err == nil {
		t.Fatal("invalid baseline accepted")
	}
}
