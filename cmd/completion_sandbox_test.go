package cmd

import (
	"slices"
	"testing"

	"github.com/pacificbelt30/codex-dock/internal/sandboxux"
	"github.com/spf13/cobra"
)

func TestGuestCompletionCandidatesUseGuestPathsAndArtifactExclusions(t *testing.T) {
	entries := []sandboxux.FileEntry{{Name: "日本語.txt", Kind: "file"}, {Name: "results", Kind: "directory"}, {Name: "link", Kind: "symlink"}, {Name: ".env", Kind: "file"}, {Name: "control\n", Kind: "file"}}
	got := guestPathCandidates(entries, "sub/", false, true)
	if !slices.Equal(got, []string{"sub/results/", "sub/日本語.txt"}) {
		t.Fatal("artifact completion offered excluded paths", got)
	}
	if got := guestPathCandidates(entries, "r", true, false); !slices.Equal(got, []string{"results/"}) {
		t.Fatal("directory completion offered files", got)
	}
}

func TestSandboxCommandsHaveContextualCompletion(t *testing.T) {
	for _, args := range [][]string{{"connect"}, {"exec"}, {"files", "get"}, {"files", "put"}, {"files", "list"}, {"artifact", "plan"}, {"artifact", "bundle"}} {
		command, _, err := rootCmd.Find(args)
		if err != nil || command.ValidArgsFunction == nil {
			t.Fatalf("%v has no positional completion: %v", args, err)
		}
		values, directive := command.ValidArgsFunction(command, []string{"worker", "already-entered"}, "")
		if len(values) != 0 || directive&cobra.ShellCompDirectiveNoFileComp == 0 {
			t.Fatalf("%v completed extra arguments with host files: %v %v", args, values, directive)
		}
	}
	for _, action := range []string{"plan", "bundle"} {
		command, _, _ := rootCmd.Find([]string{"artifact", action})
		if _, ok := command.GetFlagCompletionFunc("include"); !ok {
			t.Fatal("artifact --include has no guest file completion", action)
		}
	}
}
