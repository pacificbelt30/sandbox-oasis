package engine

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// The SDK is a client/DTO compatibility surface, not a daemon implementation.
// The remaining module-wide advisories concern daemon plugin handlers; keep
// that applicability assumption checked for shipped binaries and the Go API.
func TestNativeEntrypointsDoNotLinkDockerDaemon(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "list", "-mod=readonly", "-deps",
		"./cmd/oaiso", "./cmd/worker-guard", "./cmd/io-logger", "./pkg/cxd/...")
	cmd.Dir = "../.."
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOSUMDB=off")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect offline dependency graph: %v\n%s", err, output)
	}
	for dependency := range strings.FieldsSeq(string(output)) {
		if !strings.HasPrefix(dependency, "github.com/docker/docker/") {
			continue
		}
		switch {
		case dependency == "github.com/docker/docker/api",
			dependency == "github.com/docker/docker/api/types",
			strings.HasPrefix(dependency, "github.com/docker/docker/api/types/"),
			dependency == "github.com/docker/docker/client",
			dependency == "github.com/docker/docker/pkg/stdcopy":
		default:
			t.Errorf("native entrypoint links an unreviewed SDK component: %s", dependency)
		}
	}
}
