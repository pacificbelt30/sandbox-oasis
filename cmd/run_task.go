package cmd

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/pacificbelt30/codex-dock/internal/privatefile"
	"github.com/spf13/cobra"
)

func applyHeadlessTask(cmd *cobra.Command) error {
	if runTaskFile != "" {
		if cmd.Flags().Changed("task") {
			return fmt.Errorf("--task-file and --task are mutually exclusive")
		}
		var data []byte
		var err error
		if runTaskFile == "-" {
			data, err = io.ReadAll(io.LimitReader(cmd.InOrStdin(), 32769))
		} else {
			data, err = privatefile.Read(runTaskFile)
		}
		if err != nil {
			return err
		}
		if len(data) > 32768 || !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
			return fmt.Errorf("task file must be UTF-8 and at most 32 KiB")
		}
		runOpts.Task = string(data)
	}
	if !runHeadless {
		return nil
	}
	if runOpts.Task == "" || runOpts.ExecCmd != "" || runOpts.ShellMode || runOpts.DirectAgent {
		return fmt.Errorf("--headless needs a task and cannot combine with --exec, --shell or direct agent arguments")
	}
	switch agentFlag {
	case "codex":
		runOpts.DirectAgent = true
		runOpts.AgentArgs = []string{"exec", "--json", "--", runOpts.Task}
	case "claude":
		runOpts.DirectAgent = true
		runOpts.AgentArgs = []string{"--print", "--", runOpts.Task}
	case "shell":
		runOpts.ExecCmd = runOpts.Task
	default:
		return fmt.Errorf("--headless requires --agent codex, claude or shell")
	}
	runOpts.Task = ""
	return nil
}
