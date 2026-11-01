package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pacificbelt30/codex-dock/internal/privatefile"
	"github.com/pacificbelt30/codex-dock/internal/taskboard"
	"github.com/pacificbelt30/codex-dock/internal/ui"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var taskStoreDir string

func taskService() (*taskboard.Service, error) {
	store, err := taskboard.DefaultStore()
	if err != nil {
		return nil, err
	}
	if taskStoreDir != "" {
		store.Root, err = filepath.Abs(taskStoreDir)
		if err != nil {
			return nil, err
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	var globals []string
	config := viper.ConfigFileUsed()
	if cfgFile != "" {
		config = cfgFile
	}
	for _, item := range [][2]string{{"--config", config}, {"--auth-profile", authProfileName}, {"--auth-profile-dir", authProfileDir}, {"--agent-policy-file", agentPolicyFile}} {
		if item[1] != "" {
			value := item[1]
			if item[0] != "--auth-profile" {
				value, err = filepath.Abs(value)
				if err != nil {
					return nil, err
				}
			}
			globals = append(globals, item[0], value)
		}
	}
	fd, err := rootCmd.PersistentFlags().GetInt("passphrase-fd")
	if err != nil {
		return nil, err
	}
	return &taskboard.Service{Store: store, Runtime: &taskboard.CLIRuntime{PassphraseFD: fd, Executable: executable, GlobalArgs: globals, Store: store, Config: managementConfig()}}, nil
}
func taskJSON(cmd *cobra.Command, value any) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func init() {
	task := &cobra.Command{Use: "task", Short: "Queue isolated tasks and review results before adopting changes"}
	task.PersistentFlags().StringVar(&taskStoreDir, "store", "", "Private host task directory (default: user config directory)")
	rootCmd.AddCommand(task)
	var spec taskboard.Spec
	var promptFile string
	create := &cobra.Command{Use: "create", Short: "Queue a task from the committed project HEAD; leave the current checkout intact", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		service, err := taskService()
		if err != nil {
			return err
		}
		if promptFile != "" {
			if cmd.Flags().Changed("prompt") {
				return fmt.Errorf("choose --prompt or --prompt-file")
			}
			b, err := privatefile.Read(promptFile)
			if err != nil {
				return err
			}
			spec.Prompt = string(b)
		}
		t, err := service.Create(cmd.Context(), spec)
		if err != nil {
			return err
		}
		return taskJSON(cmd, t)
	}}
	create.Flags().StringVar(&spec.Title, "title", "", "Short task title")
	create.Flags().StringVar(&spec.Project, "project", ".", "Git repository root")
	create.Flags().StringVar(&spec.Prompt, "prompt", "", "Task instructions; prefer --prompt-file for private instructions")
	create.Flags().StringVar(&promptFile, "prompt-file", "", "Owner-private task instructions file")
	create.Flags().StringVar(&spec.Agent, "agent", "codex", "Agent: codex, claude or shell (shell executes the prompt as a command)")
	create.Flags().StringVar(&spec.Image, "image", "", "Worker image; empty preserves project/host defaults")
	create.Flags().StringVar(&spec.Policy, "agent-policy", "", "Host agent policy; empty preserves host defaults")
	create.Flags().StringVar(&spec.TestCommand, "test-command", "", "Verification command to run in a separate Kata worker")
	create.Flags().BoolVar(&spec.NoInternet, "no-internet", false, "Disable general egress for this task")
	task.AddCommand(create)
	task.AddCommand(&cobra.Command{Use: "list", Short: "List saved task records as JSON", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		s, e := taskService()
		if e != nil {
			return e
		}
		v, e := s.Store.List()
		if e != nil {
			return e
		}
		return taskJSON(cmd, v)
	}})
	for _, action := range []string{"show", "run", "retry", "stop", "sync", "test", "review"} {
		task.AddCommand(&cobra.Command{Use: action + " ID", Short: map[string]string{"show": "Show a saved task", "run": "Launch a queued task in a new isolated worktree", "retry": "Retry a failed or cancelled task from its original base", "stop": "Stop task and verification workers", "sync": "Reconcile task state with the node", "test": "Run the configured verification command in Kata", "review": "Freeze a stopped task's reviewable changes"}[action], Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			s, e := taskService()
			if e != nil {
				return e
			}
			var v any
			switch action {
			case "show":
				v, e = s.Store.Get(args[0])
			case "run", "retry":
				v, e = s.Run(cmd.Context(), args[0])
			case "stop":
				v, e = s.Stop(cmd.Context(), args[0])
			case "sync":
				v, e = s.Sync(cmd.Context(), args[0])
			case "test":
				v, e = s.Test(cmd.Context(), args[0])
			case "review":
				v, e = s.Review(cmd.Context(), args[0])
			}
			if e != nil {
				return e
			}
			return taskJSON(cmd, v)
		}})
	}
	var reviewID, target, result, selectionFile, planFile string
	plan := &cobra.Command{Use: "plan ID", Short: "Preview a commit containing selected changes on a new result branch", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, e := taskService()
		if e != nil {
			return e
		}
		r, e := s.Store.Review(reviewID)
		if e != nil {
			return e
		}
		selection := taskboard.Selection{}
		if selectionFile != "" {
			b, e := privatefile.Read(selectionFile)
			if e != nil {
				return e
			}
			if e = json.Unmarshal(b, &selection); e != nil {
				return e
			}
		} else {
			for _, c := range r.Changes {
				selection[c.Path] = nil
			}
		}
		a, e := s.Plan(cmd.Context(), args[0], reviewID, target, result, selection)
		if e != nil {
			return e
		}
		if planFile != "" {
			p, e := filepath.Abs(planFile)
			if e != nil {
				return e
			}
			b, e := json.Marshal(a)
			if e != nil {
				return e
			}
			if e = privatefile.Update(p, privatefile.Revision(nil), func([]byte) ([]byte, error) { return b, nil }); e != nil {
				return e
			}
		}
		return taskJSON(cmd, a)
	}}
	plan.Flags().StringVar(&reviewID, "review", "", "Full review ID")
	plan.Flags().StringVar(&target, "target", "main", "Existing base branch")
	plan.Flags().StringVar(&result, "branch", "", "New result branch to create on approval")
	plan.Flags().StringVar(&selectionFile, "selection-file", "", "Private JSON object mapping filenames to selected hunk IDs; [] selects whole file (default: all reviewable files)")
	plan.Flags().StringVar(&planFile, "plan-file", "", "Save the immutable plan in a new private file for task accept")
	task.AddCommand(plan)
	var acceptFile string
	var yes bool
	accept := &cobra.Command{Use: "accept ID", Short: "Create the planned result branch, preserving the current checkout and index", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if !yes {
			return fmt.Errorf("review the plan and supply --yes to create the result branch")
		}
		b, e := privatefile.Read(acceptFile)
		if e != nil {
			return e
		}
		var a taskboard.Acceptance
		if e = json.Unmarshal(b, &a); e != nil {
			return e
		}
		s, e := taskService()
		if e != nil {
			return e
		}
		t, e := s.Accept(cmd.Context(), args[0], a)
		if e != nil {
			return e
		}
		return taskJSON(cmd, t)
	}}
	accept.Flags().StringVar(&acceptFile, "plan-file", "", "Previously saved private plan file")
	accept.Flags().BoolVar(&yes, "yes", false, "Approve creation of the reviewed result branch")
	task.AddCommand(accept)
	task.AddCommand(&cobra.Command{Use: "board", Short: "Open the interactive task and result review board", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
		s, e := taskService()
		if e != nil {
			return e
		}
		return ui.RunTaskBoard(s)
	}})
}
