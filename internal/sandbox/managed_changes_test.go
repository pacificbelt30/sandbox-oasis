package sandbox

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedCollectorAndArtifactsWorkWithoutGit(t *testing.T) {
	root := t.TempDir()
	for p, data := range map[string]string{"current.txt": "working content", ".env": "excluded fixture secret", "json.py": "raise RuntimeError('must not import')"} {
		os.WriteFile(filepath.Join(root, p), []byte(data), 0600)
	}
	os.Symlink("/etc/passwd", filepath.Join(root, "escape"))
	encoded, _ := json.Marshal(root)
	program := strings.ReplaceAll(managedChangesProgram, "'/workspace'", string(encoded))
	cmd := exec.CommandContext(t.Context(), "python3", "-I", "-c", program)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "current.txt") || strings.Contains(string(out), "excluded fixture secret") || strings.Contains(string(out), "escape") || strings.Contains(string(out), ".env") {
		t.Fatal(string(out), err)
	}
	program = strings.ReplaceAll(artifactProgram, "'/workspace'", string(encoded))
	raw := `{"paths":["current.txt"],"_managed_seed":"` + strings.Repeat("a", 64) + `"}`
	cmd = exec.CommandContext(t.Context(), "python3", "-I", "-c", program, "plan", "worker", raw)
	cmd.Dir = root
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatal(string(out), err)
	}
	var plan ArtifactPlan
	if err = json.Unmarshal(out, &plan); err != nil || plan.PatchBytes != 0 || plan.Head != "workspace:"+strings.Repeat("a", 64) || len(plan.Files) != 1 {
		t.Fatal(plan, err)
	}
}
