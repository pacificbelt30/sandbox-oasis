// io-logger is the pinned containerd binary-I/O helper. The shim owns its
// lifetime, so node-service restarts cannot close a VM's console or stdin.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"syscall"
)

func run() error {
	var dir, id string
	var maxBytes int64
	flag.StringVar(&dir, "state", "", "Private container record directory")
	flag.StringVar(&id, "id", "", "Container identity")
	flag.Int64Var(&maxBytes, "max-bytes", 0, "Retain at most this many output bytes (0 keeps legacy unlimited logging)")
	flag.Parse()
	if os.Geteuid() != 0 || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(id) || !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || filepath.Base(dir) != id {
		return fmt.Errorf("invalid logger identity")
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || info.Mode().Perm()&0077 != 0 || !info.IsDir() {
		return fmt.Errorf("untrusted logger state directory")
	}
	log, err := os.OpenFile(filepath.Join(dir, "console.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()
	// Keep a writer open while a terminal detaches or the node restarts. No
	// bytes are read here: stdin belongs exclusively to the guest's reader.
	inputPath := filepath.Join(dir, "stdin.fifo")
	if info, err := os.Lstat(inputPath); err == nil {
		if info.Mode()&os.ModeNamedPipe == 0 {
			return fmt.Errorf("stdin is not a FIFO")
		}
		fd, err := syscall.Open(inputPath, syscall.O_RDWR|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		defer func() { _ = syscall.Close(fd) }()
	} else if !os.IsNotExist(err) {
		return err
	}
	stdout := os.NewFile(3, "stdout")
	stderr := os.NewFile(4, "stderr")
	ready := os.NewFile(5, "ready")
	if stdout == nil || stderr == nil || ready == nil {
		return fmt.Errorf("missing binary-I/O descriptors")
	}
	if _, err = ready.Write([]byte{1}); err != nil {
		return err
	}
	_ = ready.Close()
	var writer io.Writer = log
	if maxBytes > 0 {
		info, err := log.Stat()
		if err != nil {
			return err
		}
		if info.Size() > maxBytes {
			if err = log.Truncate(maxBytes); err != nil {
				return err
			}
			info, err = log.Stat()
			if err != nil {
				return err
			}
		}
		writer = &limitedLog{file: log, remaining: max(0, maxBytes-info.Size())}
	}
	var wg sync.WaitGroup
	var lock sync.Mutex
	var first error
	copyStream := func(input *os.File) {
		defer wg.Done()
		defer func() { _ = input.Close() }()
		_, err := io.Copy(writer, input)
		if err != nil {
			lock.Lock()
			if first == nil {
				first = err
			}
			lock.Unlock()
		}
	}
	wg.Add(2)
	go copyStream(stdout)
	go copyStream(stderr)
	wg.Wait()
	if first != nil {
		return first
	}
	return log.Sync()
}
func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "io-logger:", err)
		os.Exit(1)
	}
}

// limitedLog drains output after the retention limit so guest writes cannot
// deadlock just because the retained diagnostic file is full.
type limitedLog struct {
	mu        sync.Mutex
	file      io.Writer
	remaining int64
}

func (w *limitedLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := int64(len(p))
	keep := min(n, w.remaining)
	if keep > 0 {
		written, err := w.file.Write(p[:keep])
		w.remaining -= int64(written)
		if err != nil {
			return written, err
		}
	}
	return len(p), nil
}
