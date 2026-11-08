package taskboard

import (
	"os"
	"path/filepath"
	"testing"
)

func taskRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitTest(t, root, "init", "-b", "main")
	writeTest(t, filepath.Join(root, "private.txt"), "unrelated repository data")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-m", "private fixture")
	return root
}

func TestTaskRepositoryRejectsUnregisteredPointer(t *testing.T) {
	foreign := taskRepository(t)
	workspace := t.TempDir()
	writeTest(t, filepath.Join(workspace, ".git"), "gitdir: "+filepath.Join(foreign, ".git")+"\n")
	if repository, err := InspectRepository(t.Context(), workspace); err == nil {
		t.Fatalf("workspace pointer authorized unrelated metadata: %+v", repository)
	}
}

func TestTaskRepositoryRejectsMetadataReplacement(t *testing.T) {
	root := taskRepository(t)
	repository, err := InspectRepository(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	foreign := taskRepository(t)
	metadata := filepath.Join(root, ".git")
	if err := os.Rename(metadata, metadata+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(foreign, ".git"), metadata); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.run(t.Context(), nil, nil, "rev-parse", "HEAD"); err == nil {
		t.Fatal("persisted task followed replaced metadata into another repository")
	}
}

func TestTaskRepositoryAllowsRegisteredWorktree(t *testing.T) {
	root := taskRepository(t)
	linked := filepath.Join(t.TempDir(), "linked")
	gitTest(t, root, "worktree", "add", "--detach", linked, "HEAD")
	repository, err := InspectRepository(t.Context(), linked)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.run(t.Context(), nil, nil, "rev-parse", "HEAD"); err != nil {
		t.Fatal(err)
	}
	registration := filepath.Join(repository.GitDir, "gitdir")
	writeTest(t, registration, filepath.Join(t.TempDir(), ".git")+"\n")
	if _, err := repository.run(t.Context(), nil, nil, "rev-parse", "HEAD"); err == nil {
		t.Fatal("task ignored a changed reciprocal registration")
	}
}
