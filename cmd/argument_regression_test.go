package cmd

import "testing"

func TestGlobalCommandsRejectUnexpectedTargets(t *testing.T) {
	for _, path := range [][]string{{"proxy", "build"}, {"proxy", "run"}, {"proxy", "serve"}, {"proxy", "stop"}, {"proxy", "rm"}, {"agent-policy", "init"}, {"agent-policy", "list"}, {"agent-policy", "path"}, {"agent-policy", "validate"}, {"agent-policy", "doctor"}} {
		command, _, err := rootCmd.Find(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := command.ValidateArgs([]string{"unintended-target"}); err == nil {
			t.Errorf("%v silently ignores a target and may act globally", path)
		}
		if err := command.ValidateArgs(nil); err != nil {
			t.Errorf("%v broke its documented no-argument invocation: %v", path, err)
		}
	}
}
