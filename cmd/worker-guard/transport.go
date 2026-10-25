package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/workertransport"
)

func installTransport() error {
	var c workertransport.Config
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, (64<<10)+1))
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(&c); e != nil {
		return errors.New("invalid worker transport configuration")
	}
	if _, e := c.TLSConfig(); e != nil {
		return e
	}
	path := workertransport.ConfigPath
	dir := filepath.Dir(path)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(dir, ".transport-")
	if e != nil {
		return e
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if e = f.Chmod(0600); e == nil {
		e = json.NewEncoder(f).Encode(c)
	}
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(f.Name(), path)
}
func runTransport() error {
	f, e := os.OpenFile(workertransport.ConfigPath, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return errors.New("worker transport unavailable")
	}
	i, e := f.Stat()
	if e != nil || !i.Mode().IsRegular() || i.Mode().Perm() != 0600 || i.Size() > 64<<10 {
		_ = f.Close()
		return errors.New("unsafe worker transport configuration")
	}
	var c workertransport.Config
	e = json.NewDecoder(f).Decode(&c)
	_ = f.Close()
	if e != nil {
		return errors.New("invalid worker transport configuration")
	}
	return workertransport.Relay(context.Background(), c, func() { _, _ = io.WriteString(os.Stdout, "ready\n"); _ = os.Stdout.Close() })
}
func startTransport() error {
	command := exec.CommandContext(context.Background(), os.Args[0], "transport")
	command.Stderr = os.Stderr
	pipe, e := command.StdoutPipe()
	if e != nil {
		return e
	}
	if e = command.Start(); e != nil {
		return e
	}
	ready := make(chan bool, 1)
	go func() { line, _ := bufio.NewReader(pipe).ReadString('\n'); ready <- line == "ready\n" }()
	select {
	case ok := <-ready:
		if ok {
			_ = pipe.Close()
			return command.Process.Release()
		}
	case <-time.After(10 * time.Second):
	}
	_ = command.Process.Kill()
	_ = command.Wait()
	return errors.New("worker transport failed to start")
}
