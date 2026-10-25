package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/pacificbelt30/codex-dock/internal/buildinfo"
	"github.com/pacificbelt30/codex-dock/internal/releaseinfo"
	"github.com/spf13/cobra"
)

func init() { rootCmd.AddCommand(newUpgradeCommand()) }

func newUpgradeCommand() *cobra.Command {
	var version string
	var apply, allowDowngrade bool
	cmd := &cobra.Command{Use: "upgrade", Short: "Review a verified release installation plan; apply only with --apply", Args: cobra.NoArgs}
	cmd.Flags().StringVar(&version, "version", "", "Target release tag; defaults to the latest stable GitHub release")
	cmd.Flags().BoolVar(&apply, "apply", false, "Apply the verified host installer after planning; live resources block installation")
	cmd.Flags().BoolVar(&allowDowngrade, "allow-downgrade", false, "Explicitly allow an older signed release; the high-water mark is retained")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if os.Geteuid() == 0 {
			return fmt.Errorf("run upgrade as the normal operator; the installer elevates through sudo when applying")
		}
		if version == "" {
			latest, err := releaseinfo.Latest(cmd.Context())
			if err != nil {
				return err
			}
			version = latest.Tag
		}
		if !releaseinfo.ValidTag(version) {
			return fmt.Errorf("choose a release tag such as v0.0.2")
		}
		if apply {
			directory, err := authProfilesDirectory()
			if err != nil {
				return err
			}
			if err = rejectUnsupportedProfileDowngrade(version, directory); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Installed CLI: %s\nTarget release: %s\n\nHost update: CLI, node, helpers and pinned runtime assets.\nImages: rebuild the default worker after the host update; prepare proxies with the new CLI.\nCustom images/templates: review separately.\nBefore applying: save work, then stop/remove retained workers and proxies with the installed CLI.\n\n", buildinfo.Version, version); err != nil {
			return err
		}
		dir, err := os.MkdirTemp("", "oaiso-upgrade-*")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(dir) }()
		installer, err := releaseinfo.Installer(cmd.Context(), version, dir)
		if err != nil {
			return err
		}
		args := []string{installer, "--version", version}
		if allowDowngrade {
			args = append(args, "--allow-downgrade")
		}
		if apply {
			args = append(args, "--apply")
		}
		process := exec.CommandContext(cmd.Context(), "bash", args...)
		process.Stdin, process.Stdout, process.Stderr = cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr()
		if err := process.Run(); err != nil {
			return fmt.Errorf("release installer did not complete: %w", err)
		}
		if apply {
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "Host update completed. Log in again, then use the newly installed oaiso:\n  oaiso build --template plain --tag codex-dock:latest\n  oaiso init --agent codex\nFor Claude credentials, use: oaiso init --agent claude\nCreate new workers and check inference separately.")
		} else {
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Plan only. Apply the same release with: oaiso upgrade --version %s --apply\n", version)
		}
		return err
	}
	return cmd
}
