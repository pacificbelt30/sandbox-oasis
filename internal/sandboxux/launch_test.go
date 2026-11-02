package sandboxux

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchUsesNormalGuardedShellAndPrivateCommandInput(t *testing.T) {
	root := t.TempDir()
	spec, args, e := PrepareLaunch(LaunchSpec{Project: root, Network: "offline", Command: "printf secret-in-command"})
	if e != nil {
		t.Fatal(e)
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "secret-in-command") || !strings.Contains(joined, "--runtime kata --agent shell") || !strings.Contains(joined, "--no-ai") || !strings.Contains(joined, "--task-file -") {
		t.Fatal(args)
	}
	if spec.Name == "" {
		t.Fatal("launch has no recovery name")
	}
	script := filepath.Join(root, "launcher")
	putFile(t, script, "#!/bin/sh\nvalue=$(cat)\n[ \"$value\" = 'printf secret-in-command' ] || exit 19\ncase \"$*\" in *secret-in-command*) exit 20;; esac\nexit 17\n")
	if e = os.Chmod(script, 0700); e != nil {
		t.Fatal(e)
	}
	launcher := CLILauncher{Executable: script}
	result, e := launcher.Launch(t.Context(), spec)
	if e == nil || !strings.Contains(e.Error(), "exit status 17") || result.Name != spec.Name {
		t.Fatal(result, e)
	}
}
func TestLaunchRejectsInvalidInputsBeforeRuntimeAccess(t *testing.T) {
	for _, spec := range []LaunchSpec{{Project: t.TempDir(), Network: "allowlist"}, {Project: t.TempDir(), Network: "unknown"}, {Name: "name\n--runtime runc", Project: t.TempDir(), Network: "offline"}} {
		if _, _, e := PrepareLaunch(spec); e == nil {
			t.Fatal("invalid launch accepted", spec)
		}
	}
}

func TestBlankCommandStartsPersistentSandboxWithoutHoldingStdin(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "launcher")
	putFile(t, script, "#!/bin/sh\n[ \"$(cat)\" = 'exec sleep infinity' ] || exit 29\nexit 17\n")
	if err := os.Chmod(script, 0700); err != nil {
		t.Fatal(err)
	}
	spec := LaunchSpec{Project: root, Network: "offline"}
	_, args, err := PrepareLaunch(spec)
	if err != nil || !strings.Contains(strings.Join(args, " "), "--headless --task-file -") {
		t.Fatal("blank command did not use the persistent sandbox path", args, err)
	}
	_, err = (&CLILauncher{Executable: script}).Launch(t.Context(), spec)
	if err == nil || !strings.Contains(err.Error(), "exit status 17") {
		t.Fatal("blank command did not start the idle keeper through stdin", err)
	}
}
