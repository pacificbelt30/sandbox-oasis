package cxd

import (
	"context"
	"strings"
	"testing"
)

func TestCheckpointRestoreRejectsRuncBeforeAnyNodeCall(t *testing.T) {
	var client *Client
	if _, err := client.RestoreCheckpoint(context.Background(), strings.Repeat("a", 64), LaunchRequest{Runtime: "runc"}); err == nil {
		t.Fatal("restore weakened Kata isolation")
	}
}
