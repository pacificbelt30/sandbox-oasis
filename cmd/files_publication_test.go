package cmd

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
)

func TestArtifactBundleRemainsUnpublishedUntilExportSucceeds(t *testing.T) {
	root, err := os.MkdirTemp("", "oaiso-bundle-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	socket := filepath.Join(root, "node.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		http.Error(w, "fixture export failed", http.StatusNotFound)
	})}
	go server.Serve(listener)
	defer server.Close()
	t.Setenv("CODEX_DOCK_KATA_NODE_SOCKET", socket)
	command, _, err := rootCmd.Find([]string{"artifact", "bundle"})
	if err != nil {
		t.Fatal(err)
	}
	oldPaths, err := command.Flags().GetStringArray("include")
	if err != nil {
		t.Fatal(err)
	}
	oldPlan, _ := command.Flags().GetString("plan-id")
	oldOutput, _ := command.Flags().GetString("output")
	oldContext := command.Context()
	defer func() {
		command.SetContext(oldContext)
		_ = command.Flags().Lookup("include").Value.(pflag.SliceValue).Replace(oldPaths)
		_ = command.Flags().Set("plan-id", oldPlan)
		_ = command.Flags().Set("output", oldOutput)
	}()
	target := filepath.Join(root, "result.tar")
	_ = command.Flags().Lookup("include").Value.(pflag.SliceValue).Replace([]string{"result.txt"})
	_ = command.Flags().Set("plan-id", strings.Repeat("a", 64))
	_ = command.Flags().Set("output", target)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	command.SetContext(ctx)
	done := make(chan error, 1)
	go func() { done <- command.RunE(command, []string{"worker"}) }()
	select {
	case <-entered:
	case <-ctx.Done():
		close(release)
		t.Fatal("export never contacted fixture", ctx.Err())
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("unfinished artifact published at final path: %v", err)
	}
	close(release)
	select {
	case err := <-done:
		if err == nil {
			t.Error("failed fixture export succeeded")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed export retained destination", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".oaiso-download-") {
			t.Fatal("staging retained")
		}
	}
}
