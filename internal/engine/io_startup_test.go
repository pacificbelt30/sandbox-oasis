//go:build linux

package engine

import (
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestInteractiveStdinHasWriterBeforeLoggerStarts(t *testing.T) {
	backend := &Backend{opts: Options{Logger: "/unused-in-unit-test"}}
	creator, err := backend.logIOAt(t.TempDir(), "fixture", false, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	streams, err := creator("fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = streams.Close() }()
	fd, err := syscall.Open(streams.Config().Stdin, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Close(fd) }()
	var data [1]byte
	if _, err = syscall.Read(fd, data[:]); !errors.Is(err, syscall.EAGAIN) {
		t.Fatal("stdin observed EOF before logger/attach startup")
	}
	if err = streams.Close(); err != nil {
		t.Fatal(err)
	}
	if n, err := syscall.Read(fd, data[:]); err != nil || n != 0 {
		t.Fatal("stdin keepalive was not released on close")
	}
}

func TestRuncStdinUsesShimFileOutputAndKataKeepsBinaryLogger(t *testing.T) {
	for _, runtime := range []string{"runc", "io.containerd.kata.v2"} {
		t.Run(runtime, func(t *testing.T) {
			b := &Backend{opts: Options{State: t.TempDir(), Logger: "/unused-in-unit-test"}}
			r := &record{ID: "fixture"}
			r.Host.Runtime = runtime
			r.Config.OpenStdin = true
			creator, err := b.logIO(r)
			if err != nil {
				t.Fatal(err)
			}
			streams, err := creator("fixture")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = streams.Close() }()
			want := "binary:"
			if runtime == "runc" {
				want = "file:"
			}
			if !strings.HasPrefix(streams.Config().Stdout, want) || streams.Config().Stdin == "" {
				t.Fatal("runtime input/output scheme is incorrect")
			}
			u, err := url.Parse(streams.Config().Stdout)
			if err != nil {
				t.Fatal(err)
			}
			if runtime == "runc" {
				info, err := os.Stat(u.Path)
				if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
					t.Fatalf("runc output is not a bounded collector FIFO: %v", err)
				}
			} else if u.Query().Get("--max-bytes") != strconv.FormatInt(primaryOutputLimit, 10) {
				t.Fatal("Kata binary logger is missing the primary output limit")
			}
		})
	}
}
