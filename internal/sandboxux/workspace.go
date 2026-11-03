package sandboxux

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/pacificbelt30/codex-dock/internal/sandbox"
)

func validateCopyDestination(project, destination string) error {
	if destination == "" || !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return fmt.Errorf("choose a new absolute copy directory")
	}
	if destination == project || strings.HasPrefix(destination, project+string(filepath.Separator)) || strings.HasPrefix(project, destination+string(filepath.Separator)) {
		return fmt.Errorf("copy directory must be outside the source project")
	}
	parent, _, err := hostParent(destination)
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()
	st, err := parent.Stat()
	if err != nil {
		return err
	}
	u, ok := st.Sys().(*syscall.Stat_t)
	if !ok || u.Uid != uint32(os.Geteuid()) || st.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("copy parent must be your private directory (0700)")
	}
	if _, err = os.Lstat(destination); !os.IsNotExist(err) {
		return fmt.Errorf("copy requires a new directory; existing paths are never replaced")
	}
	return nil
}

type ArtifactBackend interface {
	PlanArtifacts(context.Context, string, sandbox.ArtifactOptions) (sandbox.ArtifactPlan, error)
	ExportArtifacts(context.Context, string, sandbox.ArtifactOptions, io.Writer) error
}

// ExportReviewedArtifacts uses a new private host file, a reviewed immutable
// plan and a host-side bound, independently of guest-provided stream sizes.
func ExportReviewedArtifacts(ctx context.Context, b ArtifactBackend, id string, options sandbox.ArtifactOptions, plan sandbox.ArtifactPlan, destination string) error {
	if plan.Worker != id || len(plan.ID) != 64 {
		return fmt.Errorf("review a plan for this sandbox first")
	}
	options.ExpectedPlan = plan.ID
	return createHostFile(destination, func(w io.Writer) error {
		bounded := &LimitedWriter{Writer: w, Remaining: 33 << 20}
		return b.ExportArtifacts(ctx, id, options, bounded)
	})
}
