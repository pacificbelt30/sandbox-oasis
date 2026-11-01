package cmd

import (
	"context"
	"debug/elf"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/proxyimage"
	"github.com/spf13/cobra"
)

func init() {
	proxyCmd.AddCommand(&cobra.Command{Use: "trust IMAGE", Short: "Authorize an explicitly reviewed proxy image digest on this node", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		c, err := engine.NewClient()
		if err != nil {
			return err
		}
		defer func() { _ = c.Close() }()
		image, err := c.ImageInspect(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		if image.Config == nil || !proxyImageCompatible(image.Config.Labels) {
			return fmt.Errorf("proxy image is incompatible with this CLI")
		}
		if err := proxyimage.Record(cmd.Context(), engine.Socket(), args[0], image.ID); err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Authorized proxy image %s at %s for this node and CLI version.\n", args[0], image.ID)
		return err
	}})
}

// An installed Linux CLI bundle is static and already matches the node. Build
// its proxy image directly from those bytes, so a project clone need not carry
// the oaiso source checkout just to refresh a stale proxy image.
func buildProxyImageFromExecutable(ctx context.Context, tag string) (bool, error) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return false, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return false, err
	}
	file, err := elf.Open(executable)
	if err != nil {
		return false, nil
	}
	static := file.Machine == elf.EM_X86_64
	for _, program := range file.Progs {
		if program.Type == elf.PT_INTERP {
			static = false
			break
		}
	}
	if err := file.Close(); err != nil {
		return false, err
	}
	if !static {
		return false, nil
	}
	dir, err := os.MkdirTemp("", "cxd-proxy-image-*")
	if err != nil {
		return true, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	src, err := os.Open(executable)
	if err != nil {
		return true, err
	}
	defer func() { _ = src.Close() }()
	copyPath := filepath.Join(dir, "codex-dock")
	dst, err := os.OpenFile(copyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
	if err != nil {
		return true, err
	}
	if _, err = io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return true, err
	}
	if err = dst.Close(); err != nil {
		return true, err
	}
	certSource, err := os.Open("/etc/ssl/certs/ca-certificates.crt")
	if err != nil {
		return true, fmt.Errorf("opening host CA bundle for static proxy image: %w", err)
	}
	defer func() { _ = certSource.Close() }()
	certDest, err := os.OpenFile(filepath.Join(dir, "ca-certificates.crt"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return true, err
	}
	if _, err = io.Copy(certDest, certSource); err != nil {
		_ = certDest.Close()
		return true, err
	}
	if err = certDest.Close(); err != nil {
		return true, err
	}
	dockerfile := filepath.Join(dir, "Dockerfile")
	contents := fmt.Sprintf(`FROM scratch
LABEL io.codex-dock.encrypted-store="stdin-key-v2-age"
ARG CODEX_DOCK_BUILD_VERSION=dev
LABEL %s=%q
LABEL %s=$CODEX_DOCK_BUILD_VERSION
COPY codex-dock /codex-dock
COPY ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
ENTRYPOINT ["/codex-dock"]
CMD ["proxy", "serve", "--listen", "0.0.0.0:18080"]
`, proxyImageProtocolLabel, proxyImageProtocolVersion, proxyImageBuildLabel)
	if err := os.WriteFile(dockerfile, []byte(contents), 0644); err != nil {
		return true, err
	}
	return true, engine.BuildProxyAt(ctx, engine.Socket(), tag, dockerfile, dir, os.Stderr)
}
