package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAtomicGuestReplacementPreservesExecutableAndAccessModes(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	program := strings.Replace(fileProgram, "os.open('/workspace',", "os.open(sys.argv[3],", 1)
	for _, mode := range []os.FileMode{0755, 0750, 0644, 0600} {
		t.Run(fmt.Sprintf("%o", mode), func(t *testing.T) {
			name := filepath.Join(root, fmt.Sprintf("script-%o", mode))
			if err := os.WriteFile(name, []byte("old"), mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(name, mode); err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(t.Context(), python, "-I", "-c", program, "write", filepath.Base(name), root)
			command.Stdin = strings.NewReader("#!/bin/sh\nprintf 'UPDATED_SCRIPT_OK\\n'\n")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("%v: %s", err, output)
			}
			st, err := os.Stat(name)
			if err != nil || st.Mode().Perm() != mode {
				t.Fatalf("replacement changed mode: got=%v want=%o err=%v", st, mode, err)
			}
			if mode&0100 != 0 {
				if output, err := exec.CommandContext(t.Context(), name).CombinedOutput(); err != nil || string(output) != "UPDATED_SCRIPT_OK\n" {
					t.Fatalf("replaced script cannot execute: %v %q", err, output)
				}
			}
		})
	}
}

func TestAtomicGuestReplacementDoesNotInheritPrivilegeBitsOrChangeAliases(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	name := filepath.Join(root, "script")
	alias := filepath.Join(root, "alias")
	if err := os.WriteFile(name, []byte("original"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(name, os.ModeSetuid|0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(name, alias); err != nil {
		t.Fatal(err)
	}
	program := strings.Replace(fileProgram, "os.open('/workspace',", "os.open(sys.argv[3],", 1)
	command := exec.CommandContext(t.Context(), python, "-I", "-c", program, "write", "script", root)
	command.Stdin = strings.NewReader("replacement")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, output)
	}
	st, err := os.Stat(name)
	if err != nil || st.Mode().Perm() != 0755 || st.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 {
		t.Fatal("replacement inherited privilege bits", st, err)
	}
	data, err := os.ReadFile(alias)
	if err != nil || string(data) != "original" {
		t.Fatal("atomic replace changed another hardlink", string(data), err)
	}
}
