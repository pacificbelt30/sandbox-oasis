package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPSRejectsInvalidOptionsBeforeNodeAccessOrSnapshotWrite(t *testing.T) {
	filter, sort, format, columns, save := psFilter, psSort, psFormat, psColumns, psSaveSnapshot
	t.Cleanup(func() { psFilter, psSort, psFormat, psColumns, psSaveSnapshot = filter, sort, format, columns, save })
	t.Setenv("CODEX_DOCK_KATA_NODE_SOCKET", filepath.Join(t.TempDir(), "absent.sock"))
	for _, tc := range []struct{ field, value, want string }{{"sort", "nonsense", "unknown instance sort"}, {"format", "nonsense", "unknown --format"}, {"columns", "nonsense", "unknown instance column"}, {"filter", "nonsense:value", "unknown"}} {
		t.Run(tc.field, func(t *testing.T) {
			psFilter, psSort, psFormat, psColumns = "", "name", "table", ""
			psSaveSnapshot = filepath.Join(t.TempDir(), "must-not-create.json")
			switch tc.field {
			case "sort":
				psSort = tc.value
			case "format":
				psFormat = tc.value
			case "columns":
				psColumns = tc.value
			case "filter":
				psFilter = tc.value
			}
			err := psCmd.RunE(psCmd, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "listing workers") {
				t.Fatal("invalid input hidden by node failure", err)
			}
			if _, err = os.Stat(psSaveSnapshot); !os.IsNotExist(err) {
				t.Fatal("invalid input wrote snapshot", err)
			}
		})
	}
	if err := psCmd.Args(psCmd, []string{"unexpected"}); err == nil {
		t.Fatal("unexpected positional argument silently ignored")
	}
}
