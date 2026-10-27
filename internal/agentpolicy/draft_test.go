package agentpolicy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDraftStagesInheritedEditsAndRejectsConcurrentSave(t *testing.T) {
	store, err := NewStore(filepath.Join(t.TempDir(), "policies", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	draft, err := OpenDraft(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := draft.Change("interactive", "approval_mode", "danger", false); err == nil {
		t.Fatal("edited a built-in")
	}
	for _, pair := range [][2]string{{"interactive", "team"}, {"team", "child"}} {
		if err := draft.Clone(pair[0], pair[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := draft.SetDefault("child"); err != nil {
		t.Fatal(err)
	}
	if err := draft.Save(); err != nil {
		t.Fatal(err)
	}
	if draft.Dirty() {
		t.Fatal("saved draft is dirty")
	}
	if err := draft.Change("team", "codex.allow_login_shell", "true", false); err != nil {
		t.Fatal(err)
	}
	if err := draft.Change("team", "extends", "child", false); err == nil {
		t.Fatal("accepted inheritance cycle")
	}
	if !strings.Contains(draft.Review(), "Policy: child") || !strings.Contains(draft.Review(), "Login shells allowed") {
		t.Fatal("review hid descendant impact or security warning")
	}
	onDisk, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	child, _ := Resolve(onDisk.File, "child")
	if child.Codex.AllowLoginShell {
		t.Fatal("staging wrote to disk")
	}
	if err := draft.Change("team", "codex.allow_login_shell", "", true); err != nil {
		t.Fatal(err)
	}
	if draft.Dirty() {
		t.Fatal("inherit did not undo the override")
	}
	if err := draft.Change("team", "codex.allow_login_shell", "false", false); err != nil {
		t.Fatal(err)
	}
	if !draft.Dirty() {
		t.Fatal("explicit false lost")
	}
	if err := store.SetDefault("review"); err != nil {
		t.Fatal(err)
	}
	if err := draft.Save(); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatal("overwrote concurrent file", err)
	}
	if !draft.Dirty() {
		t.Fatal("lost conflicting draft")
	}
	got, _ := store.Load()
	if got.File.Default != "review" {
		t.Fatal("changed concurrent writer's default")
	}
	info, _ := os.Stat(store.Path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("policy not owner-private")
	}
}

func TestDraftSaveTracksItsOwnRevision(t *testing.T) {
	store, _ := NewStore(filepath.Join(t.TempDir(), "policies", "config.toml"))
	draft, _ := OpenDraft(store)
	if err := draft.SetDefault("review"); err != nil {
		t.Fatal(err)
	}
	if err := draft.Save(); err != nil {
		t.Fatal(err)
	}
	if err := draft.SetDefault("private"); err != nil {
		t.Fatal(err)
	}
	if err := draft.Save(); err != nil {
		t.Fatal("second save failed", err)
	}
	file, _ := store.Load()
	if file.File.Default != "private" || draft.Dirty() {
		t.Fatal("save revision not updated")
	}
}
