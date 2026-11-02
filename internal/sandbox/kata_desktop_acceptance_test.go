package sandbox_test

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/pacificbelt30/codex-dock/internal/sandboxux"
)

//go:embed testdata/sandbox-desktop-probe.py
var sandboxDesktopProbe string

func TestKataSandboxDesktop(t *testing.T) {
	if os.Getenv("CODEX_DOCK_KATA_E2E") != "1" {
		t.Skip("requires disposable Linux/KVM setup acceptance")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	binary, image := os.Getenv("CODEX_DOCK_KATA_TEST_BINARY"), os.Getenv("CODEX_DOCK_KATA_TEST_IMAGE")
	if binary == "" || image == "" {
		t.Fatal("matching CLI and worker image are required")
	}
	root, err := os.MkdirTemp("/var/tmp", "sui-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	project := filepath.Join(root, "project")
	if err = os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	source, download := filepath.Join(root, "source.txt"), filepath.Join(root, "downloaded.txt")
	for p, data := range map[string]string{source: "UPLOAD_FROM_HOST\n日本語 [blue]literal\n", filepath.Join(project, "hello.txt"): "HELLO_FROM_SANDBOX\n日本語 [red]literal\n"} {
		if err = os.WriteFile(p, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.Symlink("/etc/passwd", filepath.Join(project, "escape")); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("sandbox-ui-%d", time.Now().UnixNano())
	manager, err := sandbox.NewManager(sandbox.ManagerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	node, err := engine.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close() })
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, err := node.ContainerInspect(clean, name); errdefs.IsNotFound(err) {
			return
		} else if err != nil {
			t.Error(err)
			return
		}
		if err := manager.RemoveContext(clean, name, true); err != nil {
			t.Error(err)
		}
	})
	command := exec.CommandContext(ctx, "python3", "-c", sandboxDesktopProbe, binary, project, image, name, source, download)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("public sandbox TUI: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
	inspected, err := node.ContainerInspect(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	if inspected.Config.Labels["codex-dock.proxy-container-url"] != "" {
		t.Fatal("pure sandbox gained an AI proxy")
	}
	for _, env := range inspected.Config.Env {
		for _, prefix := range []string{"CODEX_TOKEN=", "CODEX_AUTH_PROXY_URL=", "OPENAI_API_KEY=", "ANTHROPIC_API_KEY="} {
			if strings.HasPrefix(env, prefix) {
				t.Fatal("AI credentials configured in pure sandbox")
			}
		}
	}
	if inspected.State == nil || !inspected.State.Running {
		t.Fatal("TUI quit stopped the sandbox unexpectedly")
	}
	usage, err := manager.RootFSUsage(ctx, inspected.ID)
	if err != nil || !usage.Available || usage.LimitBytes != 4<<30 {
		t.Fatalf("host quota diagnostics: %+v %v", usage, err)
	}
	t.Log("verified: no AI credential/proxy dependency, guarded Kata execution, kernel rootfs quota, retained sandbox and workspace after UI exit")
	t.Run("CredentialFreeHostEgress", func(t *testing.T) {
		previous, err := node.ContainerList(ctx, container.ListOptions{All: true})
		if err != nil {
			t.Fatal(err)
		}
		known := map[string]bool{}
		for _, c := range previous {
			known[c.ID] = true
		}
		networkName := name + "-network"
		t.Cleanup(func() {
			clean, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if _, err := node.ContainerInspect(clean, networkName); err == nil {
				if err = manager.RemoveContext(clean, networkName, true); err != nil {
					t.Error(err)
				}
			} else if !errdefs.IsNotFound(err) {
				t.Error(err)
			}
			current, err := node.ContainerList(clean, container.ListOptions{All: true})
			if err != nil {
				t.Error(err)
				return
			}
			for _, c := range current {
				if known[c.ID] {
					continue
				}
				for _, n := range c.Names {
					if strings.HasPrefix(strings.TrimPrefix(n, "/"), "codex-http-policy-") {
						seconds := 5
						if err = node.ContainerStop(clean, c.ID, container.StopOptions{Timeout: &seconds}); err != nil {
							t.Error(err)
						}
						if err = node.ContainerRemove(clean, c.ID, container.RemoveOptions{Force: true}); err != nil {
							t.Error(err)
						}
					}
				}
			}
		})
		config := filepath.Join(root, "network.toml")
		data := "auto_build = false\nproxy_image = " + strconv.Quote(os.Getenv("CODEX_DOCK_KATA_PROXY_IMAGE")) + "\n[proxy]\nforward_default_deny = false\nforward_deny_domains = [\"blocked.example\"]\n"
		if err = os.WriteFile(config, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		launcher := &sandboxux.CLILauncher{Executable: binary, GlobalArgs: []string{"--config", config}}
		if _, err := launcher.Launch(ctx, sandboxux.LaunchSpec{Name: networkName, Project: project, Image: image, Network: "host"}); err != nil {
			t.Fatalf("persistent sandbox with credential-free egress: %v", err)
		}
		info, err := node.ContainerInspect(ctx, networkName)
		if err != nil {
			t.Fatal(err)
		}
		if info.Config.Labels["codex-dock.proxy-container-url"] != "" {
			t.Fatal("general egress gained an AI proxy")
		}
		check := exec.CommandContext(ctx, binary, "exec", networkName, "--", "sh", "-c", `test -n "$HTTP_PROXY" && test -z "${OPENAI_API_KEY:-}${CODEX_TOKEN:-}" && test "$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 -x "$HTTP_PROXY" http://10.255.255.1/)" = 403 && test "$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 -x "$HTTP_PROXY" http://blocked.example/)" = 403`)
		if out, err := check.CombinedOutput(); err != nil {
			t.Fatalf("credential-free policy enforcement: %v %s", err, out)
		}
		t.Log("verified: blank-command sandbox stays running; host-policy general egress without AI credentials; private IP and host-denied domain refused without external traffic")
	})
}
