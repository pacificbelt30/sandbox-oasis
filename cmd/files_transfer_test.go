package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFailedFileAndDiffDownloadsDoNotPublishEmptyDestinations(t *testing.T) {
	t.Setenv("CODEX_DOCK_KATA_NODE_SOCKET", filepath.Join(t.TempDir(), "missing.sock"))
	for _, path := range [][]string{{"files", "get"}, {"files", "list"}, {"artifact", "diff"}} {
		name := path[0] + "-" + path[1]
		t.Run(name, func(t *testing.T) {
			cmd, _, err := rootCmd.Find(path)
			if err != nil {
				t.Fatal(err)
			}
			previousContext := cmd.Context()
			cmd.SetContext(t.Context())
			defer cmd.SetContext(previousContext)
			flag := "local"
			args := []string{"absent", "file.txt"}
			if path[0] == "artifact" {
				flag = "output"
				args = []string{"absent"}
			}
			previous, err := cmd.Flags().GetString(flag)
			if err != nil {
				t.Fatal(err)
			}
			defer cmd.Flags().Set(flag, previous)
			dir := t.TempDir()
			target := filepath.Join(dir, "result")
			if err = cmd.Flags().Set(flag, target); err != nil {
				t.Fatal(err)
			}
			if err = cmd.RunE(cmd, args); err == nil {
				t.Fatal("unreachable node reported successful download")
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatal("failed transfer left a destination or staging file", entries, err)
			}
		})
	}
}
