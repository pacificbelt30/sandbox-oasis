package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/pacificbelt30/codex-dock/internal/brokersettings"
	"github.com/pacificbelt30/codex-dock/internal/ui"
	"github.com/spf13/cobra"
)

func init() { rootCmd.AddCommand(newBrokerCommand()) }

func newBrokerCommand() *cobra.Command {
	var file, template, origin, scope string
	var secretStdin bool
	root := &cobra.Command{Use: "broker", Short: "Configure fixed-origin credential routes without displaying secrets"}
	root.PersistentFlags().StringVar(&file, "file", "", "Private broker JSON file (default ~/.config/codex-dock/broker/routes.json)")
	resolve := func() (string, error) {
		if file == "" {
			return brokersettings.DefaultPath()
		}
		return filepath.Abs(file)
	}
	root.AddCommand(&cobra.Command{Use: "templates", Short: "List safe starting templates", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "bearer-readonly   Authorization: Bearer <secret>; GET, HEAD\napi-key-readonly  X-Api-Key: <secret>; GET, HEAD\nBoth require an explicit HTTPS origin and path scope.")
		return err
	}})
	root.AddCommand(&cobra.Command{Use: "check", Short: "Validate private file and scopes locally (no DNS, connection or authentication test)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		path, err := resolve()
		if err != nil {
			return err
		}
		routes, _, err := brokersettings.Load(path)
		if err != nil {
			return err
		}
		if len(routes) == 0 {
			return fmt.Errorf("no configured broker routes")
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(routes)
	}})
	set := &cobra.Command{Use: "set NAME", Short: "Create/update a scoped read-only route; read a secret only from stdin", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		path, err := resolve()
		if err != nil {
			return err
		}
		d, err := brokersettings.Template(template, args[0], origin, scope)
		if err != nil {
			return err
		}
		_, revision, err := brokersettings.Load(path)
		if err != nil {
			return err
		}
		secret := ""
		if secretStdin {
			data, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), (64<<10)+2))
			if err != nil {
				return fmt.Errorf("secret input failed")
			}
			defer clear(data)
			secret = strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
			if secret == "" || len(secret) > 64<<10 {
				return fmt.Errorf("secret input is empty or too large")
			}
		}
		if err := brokersettings.Save(path, revision, d, secret); err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "Saved. Validate with broker check; recreate drained auth proxies explicitly to apply.")
		return err
	}}
	set.Flags().StringVar(&template, "template", "bearer-readonly", "bearer-readonly or api-key-readonly")
	set.Flags().StringVar(&origin, "origin", "", "Fixed HTTPS origin (required)")
	set.Flags().StringVar(&scope, "path", "", "Allowed path and descendants (required)")
	set.Flags().BoolVar(&secretStdin, "secret-stdin", false, "Read a new/replacement secret from stdin; omission preserves existing secret")
	root.AddCommand(set, &cobra.Command{Use: "configure", Short: "Edit broker routes in the TUI (node not required)", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
		path, err := resolve()
		if err != nil {
			return err
		}
		return ui.RunBrokerSettings(path)
	}})
	return root
}
