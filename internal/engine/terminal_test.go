//go:build linux

package engine

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestTerminalFIFORecoversBufferedOutputAndAppendsHistory(t *testing.T) {
	dir := t.TempDir()
	first, err := openTerminalIO(dir, &loggingIO{}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	path := first.Config().Stdout
	if !filepath.IsAbs(path) || first.Config().Stderr != "" {
		t.Fatal("not a passfd terminal FIFO")
	}
	// Kata retains this read descriptor while the node is absent.
	keeper, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = keeper.Close() }()
	writer, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()
	prefix := strings.Repeat("first\n", 10000)
	if _, err := writer.WriteString(prefix); err != nil {
		t.Fatal(err)
	}
	first.Cancel()
	if _, err := writer.WriteString("during-restart\n"); err != nil {
		t.Fatal(err)
	}
	second, err := openTerminalIO(dir, &loggingIO{}, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString("after-restart\n"); err != nil {
		t.Fatal(err)
	}
	second.Wait()
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "console.log"))
	if err != nil || string(data) != prefix+"during-restart\nafter-restart\n" {
		t.Fatalf("output lost across collector recovery: %d bytes %v", len(data), err)
	}
}

func TestTerminalOutputRejectsUnexpectedFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "terminal.fifo"), []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := openTerminalIO(dir, &loggingIO{}, 64); err == nil {
		output.Cancel()
		t.Fatal("adopted an ordinary file")
	}
}

func TestTerminalOutputLimitPersistsAcrossCollectorRestart(t *testing.T) {
	dir := t.TempDir()
	const limit = 64
	first, err := openTerminalIO(dir, &loggingIO{}, limit)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := os.OpenFile(first.Config().Stdout, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.WriteString(strings.Repeat("x", 256)); err != nil {
		t.Fatal(err)
	}
	first.Wait()

	second, err := openTerminalIO(dir, &loggingIO{}, limit)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.WriteString(strings.Repeat("y", 256)); err != nil {
		t.Fatal(err)
	}
	second.Wait()
	_ = writer.Close()
	data, err := os.ReadFile(filepath.Join(dir, "console.log"))
	if err != nil || len(data) != limit || string(data) != strings.Repeat("x", limit) {
		t.Fatalf("retained %d bytes across restart: %v", len(data), err)
	}
}

func TestRuncStdinOutputUsesBoundedFIFO(t *testing.T) {
	dir := t.TempDir()
	const limit = 32
	output, err := openRuncStdinIO(dir, &loggingIO{}, limit)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(output.Config().Stdout)
	if err != nil || u.Scheme != "file" || output.Config().Stderr != output.Config().Stdout {
		t.Fatalf("unexpected runc output URI: %q %v", output.Config().Stdout, err)
	}
	info, err := os.Stat(u.Path)
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("runc output is not a FIFO: %v", err)
	}
	writer, err := os.OpenFile(u.Path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = writer.WriteString(strings.Repeat("z", 256)); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	output.Wait()
	data, err := os.ReadFile(filepath.Join(dir, "console.log"))
	if err != nil || string(data) != strings.Repeat("z", limit) {
		t.Fatalf("runc retained %d bytes: %v", len(data), err)
	}
}

func TestOutputCollectorTruncatesLegacyOversizedLog(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "console.log"), []byte(strings.Repeat("x", 128)), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := openTerminalIO(dir, &loggingIO{}, 32)
	if err != nil {
		t.Fatal(err)
	}
	output.Wait()
	info, err := os.Stat(filepath.Join(dir, "console.log"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 32 {
		t.Fatalf("legacy log size=%d", info.Size())
	}
}

func TestEnforceOutputRetentionCapsLegacyBinaryLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "console.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 128)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := enforceOutputRetention(dir, 32); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 32 {
		t.Fatalf("legacy binary log size=%d", info.Size())
	}
}
