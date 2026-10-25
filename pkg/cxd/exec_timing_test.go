package cxd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestExecWithTimingsPreservesValidationFailure(t *testing.T) {
	c := &Client{}
	request := ExecRequest{Argv: []string{"private-command"}, WorkingDir: "private-relative-path"}
	code, timing, err := c.ExecWithTimings(context.Background(), "private-worker", request)
	ordinaryCode, ordinary := c.Exec(context.Background(), "private-worker", request)
	if err == nil || ordinary == nil || err.Error() != ordinary.Error() || code != ordinaryCode {
		t.Fatalf("outcome changed: %d %v / %d %v", code, err, ordinaryCode, ordinary)
	}
	raw, _ := json.Marshal(timing)
	if strings.Contains(string(raw), "private") || timing.Duration < 0 || len(timing.Phases) == 0 {
		t.Fatalf("invalid or sensitive timing: %s", raw)
	}
}
