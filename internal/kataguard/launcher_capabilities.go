package kataguard

import (
	"context"
	"encoding/json"
	"os/exec"
	"slices"
	"time"
)

func launcherTemporaryStorage(path string) bool {
	return launcherFeature(path, "temporary-storage-probe-v1")
}

func launcherFeature(path, feature string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, path, "capabilities").Output()
	if err != nil {
		return false
	}
	var features []string
	return json.Unmarshal(data, &features) == nil && slices.Contains(features, feature)
}
