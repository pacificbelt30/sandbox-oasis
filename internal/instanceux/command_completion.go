package instanceux

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/pacificbelt30/codex-dock/internal/sandboxux"
)

func ShellBuiltins() []string {
	return []string{"alias", "cd", "command", "echo", "env", "exit", "export", "printf", "pwd", "read", "set", "test", "true", "type", "umask", "unalias", "unset", "wait"}
}

const commandNamesProgram = `
import os,json,re,stat,sys
names=set()
directories=set()
prefix=sys.argv[1]
for directory in os.environ.get('PATH','/usr/local/bin:/usr/bin:/bin').split(':')[:32]:
    if not directory.startswith('/'):continue
    directory=os.path.realpath(directory)
    if directory in directories:continue
    directories.add(directory)
    try:
        with os.scandir(directory) as entries:
            for i,entry in enumerate(entries):
                if i>=10000 or len(names)>=2048:break
                if not entry.name.startswith(prefix) or entry.name in names:continue
                if len(entry.name)>128 or not re.fullmatch(r'[A-Za-z_][A-Za-z0-9_.+-]*',entry.name):continue
                try:
                    if stat.S_ISREG(entry.stat().st_mode) and os.access(entry.path,os.X_OK):names.add(entry.name)
                except OSError:pass
    except OSError:pass
print(json.dumps(sorted(names)))
`

type CommandExecutor interface {
	ExecWithOptions(context.Context, string, sandbox.ExecOptions) (int64, error)
}

// Discover executable names inside the VM without running shell profiles,
// completion scripts or any of the candidate programs.
func CommandNames(ctx context.Context, guest CommandExecutor, id string) ([]string, error) {
	return CommandNamesMatching(ctx, guest, id, "")
}

// CommandNamesMatching filters before file metadata checks, keeping shell
// completion responsive even when the guest filesystem has slow metadata I/O.
func CommandNamesMatching(ctx context.Context, guest CommandExecutor, id, prefix string) ([]string, error) {
	if prefix != "" && !safeCommandName(prefix) {
		return nil, fmt.Errorf("invalid command prefix")
	}
	var out bytes.Buffer
	code, err := guest.ExecWithOptions(ctx, id, sandbox.ExecOptions{Argv: []string{"python3", "-I", "-c", commandNamesProgram, prefix}, WorkingDir: "/workspace", Stdout: &sandboxux.LimitedWriter{Writer: &out, Remaining: 64 << 10}})
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("guest command discovery exited %d", code)
	}
	var names []string
	if err := json.Unmarshal(out.Bytes(), &names); err != nil || len(names) > 2048 {
		return nil, fmt.Errorf("invalid command discovery response")
	}
	var result []string
	for _, name := range ShellBuiltins() {
		if strings.HasPrefix(name, prefix) {
			result = append(result, name)
		}
	}
	for _, name := range names {
		if safeCommandName(name) && strings.HasPrefix(name, prefix) {
			result = append(result, name)
		}
	}
	slices.Sort(result)
	return slices.Compact(result), nil
}

func safeCommandName(name string) bool {
	if name == "" || len(name) > 128 {
		return false
	}
	for i, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '_' || i > 0 && (r >= '0' && r <= '9' || strings.ContainsRune(".-+", r)) {
			continue
		}
		return false
	}
	return true
}

func CommandSuggestions(prefix string, names []string) []string {
	if !safeCommandName(prefix) {
		return nil
	}
	var result []string
	for _, name := range names {
		if safeCommandName(name) && name != prefix && strings.HasPrefix(name, prefix) {
			result = append(result, name)
		}
	}
	slices.Sort(result)
	return slices.Compact(result)
}
