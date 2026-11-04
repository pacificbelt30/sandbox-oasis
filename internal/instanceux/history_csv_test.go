package instanceux

import (
	"bytes"
	"encoding/csv"
	"errors"
	"strings"
	"testing"
)

func TestSnapshotDiffCSVPreservesValuesAndEmptyResults(t *testing.T) {
	diff := SnapshotDiff{Changes: []WorkerChange{{Kind: ChangeAdded, ID: strings.Repeat("a", 64), Name: "日本語,worker"}, {Kind: ChangeUpdated, ID: "b", Name: "another", Values: []ValueChange{{Field: "task", Before: "a,\"quoted\"\nline", After: "日本語\nnew"}, {Field: "image", Before: "old", After: "new"}}}}}
	var out bytes.Buffer
	if err := WriteDiffCSV(&out, diff); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&out).ReadAll()
	if err != nil || len(rows) != 4 {
		t.Fatal(rows, err)
	}
	if strings.Join(rows[0], ",") != "change,id,name,field,before,after" || rows[1][2] != "日本語,worker" || rows[2][4] != "a,\"quoted\"\nline" || rows[2][5] != "日本語\nnew" {
		t.Fatal(rows)
	}
	out.Reset()
	if err := WriteDiffCSV(&out, SnapshotDiff{}); err != nil {
		t.Fatal(err)
	}
	rows, err = csv.NewReader(&out).ReadAll()
	if err != nil || len(rows) != 1 {
		t.Fatal("empty CSV must contain only its header", rows, err)
	}
	want := errors.New("broken output")
	if err := WriteDiffCSV(failedDiffWriter{want}, diff); !errors.Is(err, want) {
		t.Fatal("output error swallowed", err)
	}
}

type failedDiffWriter struct{ err error }

func (w failedDiffWriter) Write([]byte) (int, error) { return 0, w.err }
