package localapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/pkg/cxd"
	"github.com/pacificbelt30/codex-dock/pkg/cxd/credentialstore"
	"github.com/pacificbelt30/codex-dock/pkg/cxd/identity"
)

type sdkStore struct{}

func (sdkStore) Load(context.Context, string) (credentialstore.Credentials, error) {
	return credentialstore.Credentials{Version: "1", ConnectionVersion: "1", OpenAIAPIKey: "fixture-sdk-api-key-never-valid"}, nil
}
func (sdkStore) SaveRefresh(context.Context, string, string, string, credentialstore.Credentials) (string, error) {
	return "", credentialstore.ErrConflict
}

// This suite must be explicitly selected on a disposable node. SDK runtimes
// and sources are test assets, not installed product dependencies.
func TestKataLocalSDK(t *testing.T) {
	if os.Getenv("OAISO_LOCAL_SDK_KATA") != "1" {
		t.Skip("set OAISO_LOCAL_SDK_KATA=1 on an isolated matching Kata node")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	assets := os.Getenv("OAISO_LOCAL_SDK_ASSETS")
	if assets == "" {
		t.Fatal("OAISO_LOCAL_SDK_ASSETS is required")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(root, "store.sock")
	storeCtx, stopStore := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- credentialstore.Serve(storeCtx, socket, "sdk-case", sdkStore{}) }()
	defer func() {
		stopStore()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	secret := strings.Repeat("sdk-fixture-admin-", 3)
	proxy, err := cxd.EnsureCaseProxy(ctx, cxd.CaseProxyOptions{Name: fmt.Sprintf("sdk-api-%d", time.Now().UnixNano()), CaseID: "sdk-case", StoreSocket: socket, AdminPort: port, AdminSecret: secret, Image: os.Getenv("CODEX_DOCK_KATA_API_PROXY_IMAGE")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		if err := cxd.RemoveCaseProxy(cleanup, engine.Socket(), proxy.Name, "sdk-case"); err != nil {
			t.Error(err)
		}
	}()
	options := cxd.Options{NodeSocket: engine.Socket(), ProxyAdminURL: proxy.AdminURL, ProxyContainerURL: proxy.ContainerURL, ProxyAdminSecret: secret, StateDir: filepath.Join(root, "state")}
	client, err := cxd.Open(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, language := range []string{"python", "typescript"} {
		t.Run(language, func(t *testing.T) {
			project := filepath.Join(root, language)
			if err := os.Mkdir(project, 0755); err != nil {
				t.Fatal(err)
			}
			requestID := fmt.Sprintf("sdk/%s/%d", language, time.Now().UnixNano())
			lostID := requestID + "/lost"
			defer func() {
				cleanup, stop := context.WithTimeout(context.Background(), time.Minute)
				defer stop()
				for _, id := range []string{requestID, lostID} {
					if err := client.RemoveRequest(cleanup, id, true); err != nil && !errors.Is(err, cxd.ErrNotFound) {
						t.Error("fallback cleanup:", err)
					}
				}
			}()
			data, err := json.Marshal(map[string]any{"options": options, "request": cxd.LaunchRequest{RequestID: requestID, ProjectDir: project, Image: os.Getenv("CODEX_DOCK_KATA_TEST_IMAGE"), Profile: "compact", User: fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), NoGitMetadata: true, NoInternet: true, Identity: identity.Identity{CaseID: "sdk-case", RunID: requestID, ConnectionVersion: "1", Provider: "openai"}}, "binary": os.Getenv("CODEX_DOCK_KATA_TEST_BINARY"), "mismatched_binary": "/bundle-next/oaiso", "lost_request_id": lostID, "secrets": []string{secret, "fixture-sdk-api-key-never-valid"}})
			if err != nil {
				t.Fatal(err)
			}
			var command *exec.Cmd
			if language == "python" {
				command = exec.CommandContext(ctx, "python3", filepath.Join(assets, "python/tests/kata.py"))
			} else {
				command = exec.CommandContext(ctx, filepath.Join(assets, "node"), filepath.Join(assets, "typescript/test/kata.mjs"))
			}
			command.Env = append(os.Environ(), "PYTHONPATH="+filepath.Join(assets, "python"))
			command.Stdin = bytes.NewReader(data)
			out, err := command.CombinedOutput()
			if bytes.Contains(out, []byte(secret)) || bytes.Contains(out, []byte("fixture-sdk-api-key-never-valid")) {
				t.Fatal("SDK output exposed fixture credentials")
			}
			if err != nil {
				t.Fatalf("SDK failed: %v\n%s", err, out)
			}
			t.Log(strings.TrimSpace(string(out)))
		})
	}
}
