package engine

import (
	"context"
	"slices"
	"strings"

	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/pkg/oci"
)

// containerd's WithTTY appends TERM=xterm after the caller's environment.
// Preserve an explicit image/worker TERM while retaining the OCI TTY defaults
// for callers that did not declare terminal capabilities.
func withWorkerTTY(ctx context.Context, client oci.Client, container *containers.Container, spec *oci.Spec) error {
	var environment []string
	configured := false
	if spec.Process != nil {
		environment = slices.Clone(spec.Process.Env)
		for _, value := range environment {
			configured = configured || strings.HasPrefix(value, "TERM=")
		}
	}
	if err := oci.WithTTY(ctx, client, container, spec); err != nil {
		return err
	}
	if configured {
		spec.Process.Env = environment
	}
	return nil
}
