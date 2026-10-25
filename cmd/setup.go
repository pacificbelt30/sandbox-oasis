package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"

	kataassets "github.com/pacificbelt30/codex-dock/deploy/kata"
	"github.com/spf13/cobra"
)

func init() {
	var bundle, operator, cache, python string
	var apply, offline, rollback, allowDowngrade bool
	setup := &cobra.Command{Use: "setup", Short: "Plan or apply a verified Ubuntu 26.04 x86_64 node install/update", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) (runErr error) {
		if apply && os.Geteuid() != 0 {
			return fmt.Errorf("--apply needs root to install services; run the displayed plan first and use sudo for setup only")
		}
		if !rollback {
			var err error
			bundle, err = setupBundle(bundle)
			if err != nil {
				return err
			}
			operator, err = setupOperator(operator, apply)
			if err != nil {
				return err
			}
		}
		interpreter, err := setupPython(cmd.Context(), python, offline)
		if err != nil {
			return err
		}
		dir, err := os.MkdirTemp("", "codex-dock-setup-")
		if err != nil {
			return err
		}
		defer func() { runErr = errors.Join(runErr, os.RemoveAll(dir)) }()
		entries, err := kataassets.Files.ReadDir(".")
		if err != nil {
			return err
		}
		for _, entry := range entries {
			data, err := kataassets.Files.ReadFile(entry.Name())
			if err != nil {
				return err
			}
			if err = os.WriteFile(filepath.Join(dir, entry.Name()), data, 0600); err != nil {
				return err
			}
		}
		argv := []string{filepath.Join(dir, "setup.py"), "--bundle", bundle, "--operator", operator, "--cache", cache}
		if apply {
			argv = append(argv, "--apply")
		}
		if offline {
			argv = append(argv, "--offline")
		}
		if allowDowngrade {
			argv = append(argv, "--allow-downgrade")
		}
		if rollback {
			argv = append(argv, "--rollback")
		}
		child := exec.CommandContext(cmd.Context(), interpreter, append([]string{"-E", "-s"}, argv...)...)
		child.Stdin = cmd.InOrStdin()
		child.Stdout = cmd.OutOrStdout()
		child.Stderr = cmd.ErrOrStderr()
		if err = child.Run(); err != nil {
			return fmt.Errorf("node setup: %w", err)
		}
		return nil
	}}
	setup.Flags().StringVar(&bundle, "bundle", "", "Directory containing prebuilt CLI/helpers and bundle.json (default: beside this CLI)")
	setup.Flags().StringVar(&python, "python", "", "Absolute path to a trusted Python 3.11+ executable (default: uv-managed Python 3.12; resolve before sudo)")
	setup.Flags().StringVar(&operator, "operator", "", "Existing non-root account granted root-equivalent node access (default: current user for a plan)")
	setup.Flags().StringVar(&cache, "cache", "/var/cache/codex-dock-setup", "Verified upstream archive cache")
	setup.Flags().BoolVar(&apply, "apply", false, "Install/update the dedicated services (default prints a plan)")
	setup.Flags().BoolVar(&offline, "offline", false, "Require cached archives and already installed system packages")
	setup.Flags().BoolVar(&allowDowngrade, "allow-downgrade", false, "Explicitly allow a release older than the installed high-water mark")
	setup.Flags().BoolVar(&rollback, "rollback", false, "Restore the previous successful setup transaction after all containers are removed")
	rootCmd.AddCommand(setup)
}

// Resolve a local interpreter without downloads or project configuration.
// The installer passes its selected absolute path across sudo.
func setupPython(ctx context.Context, explicit string, offline bool) (string, error) {
	if explicit == "" {
		args := []string{"python", "find", "--no-project", "--no-config", "--managed-python", "--system", "--no-python-downloads", "3.12"}
		if offline {
			args = append(args, "--offline")
		}
		output, err := exec.CommandContext(ctx, "uv", args...).Output()
		if err != nil {
			return "", fmt.Errorf("find uv-managed Python 3.12: %w; use scripts/install.sh, or pass --python with the absolute path from uv python find before sudo", err)
		}
		explicit = strings.TrimSpace(string(output))
	}
	if !filepath.IsAbs(explicit) {
		return "", fmt.Errorf("--python must be an absolute path to a trusted Python 3.11+ executable")
	}
	resolved, err := filepath.EvalSymlinks(explicit)
	if err != nil {
		return "", fmt.Errorf("resolve setup Python: %w", err)
	}
	check := exec.CommandContext(ctx, resolved, "-E", "-s", "-c", "import hashlib,sys,tomllib; sys.exit(sys.version_info < (3,11))")
	if err = check.Run(); err != nil {
		return "", fmt.Errorf("setup needs a working Python 3.11+: %w", err)
	}
	return resolved, nil
}

func setupBundle(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("find CLI bundle: %w; pass --bundle PATH", err)
	}
	return setupBundleBeside(executable)
}

func setupBundleBeside(executable string) (string, error) {
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", fmt.Errorf("find CLI bundle: %w; pass --bundle PATH", err)
	}
	dir := filepath.Dir(resolved)
	if _, err := os.Stat(filepath.Join(dir, "bundle.json")); err != nil {
		return "", fmt.Errorf("no node bundle beside this CLI (%s); pass --bundle PATH from make node-bundle", dir)
	}
	return dir, nil
}

func setupOperator(explicit string, apply bool) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if apply || os.Geteuid() == 0 {
		return "", fmt.Errorf("pass --operator USER when applying setup as root; this grants root-equivalent node access")
	}
	current, err := user.Current()
	if err != nil || current.Username == "" {
		return "", fmt.Errorf("cannot determine the current account; pass --operator USER")
	}
	return current.Username, nil
}
