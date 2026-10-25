//go:build linux

package engine

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Kata's primary RPC terminal path can lose output and never report exit.
// A FIFO selects passfd instead. The shim keeps its own FIFO reader open,
// retaining buffered output while the node restarts; recovery resumes draining.
type terminalIO struct {
	*loggingIO
	stop chan struct{}
	done chan struct{}
	once sync.Once
}

func openTerminalIO(dir string, base *loggingIO, limit int64) (*terminalIO, error) {
	t, path, err := openOutputCollector(dir, "terminal.fifo", base, limit)
	if err != nil {
		return nil, err
	}
	t.config.Stdout, t.config.Stderr = path, ""
	return t, nil
}

func openRuncStdinIO(dir string, base *loggingIO, limit int64) (*terminalIO, error) {
	t, path, err := openOutputCollector(dir, "output.fifo", base, limit)
	if err != nil {
		return nil, err
	}
	output := (&url.URL{Scheme: "file", Path: path}).String()
	t.config.Stdout, t.config.Stderr = output, output
	return t, nil
}

func openOutputCollector(dir, name string, base *loggingIO, limit int64) (*terminalIO, string, error) {
	path := filepath.Join(dir, name)
	if err := syscall.Mkfifo(path, 0600); err != nil && !os.IsExist(err) {
		return nil, "", err
	}
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, "", err
	}
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFIFO {
		_ = unix.Close(fd)
		return nil, "", fmt.Errorf("worker output is not a FIFO")
	}
	log, err := os.OpenFile(filepath.Join(dir, "console.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		_ = unix.Close(fd)
		return nil, "", err
	}
	writer, err := newRetainedLog(log, limit)
	if err != nil {
		_ = log.Close()
		_ = unix.Close(fd)
		return nil, "", err
	}
	t := &terminalIO{loggingIO: base, stop: make(chan struct{}), done: make(chan struct{})}
	go t.collect(fd, log, writer)
	return t, path, nil
}

func (t *terminalIO) collect(fd int, log *os.File, writer io.Writer) {
	defer close(t.done)
	defer func() { _ = unix.Close(fd); _ = log.Sync(); _ = log.Close() }()
	buf := make([]byte, 32768)
	var drainDeadline time.Time
	for {
		select {
		case <-t.stop:
			if drainDeadline.IsZero() {
				drainDeadline = time.Now().Add(250 * time.Millisecond)
			}
			if time.Now().After(drainDeadline) {
				return
			}
		default:
		}
		n, err := unix.Read(fd, buf)
		if n > 0 {
			if _, writeErr := writer.Write(buf[:n]); writeErr != nil {
				return
			}
			continue
		}
		if err != nil && err != unix.EAGAIN && err != unix.EINTR {
			return
		}
		select {
		case <-t.stop:
			return // Drain available bytes before stopping.
		default:
		}
		_, _ = unix.Poll([]unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}, 50)
	}
}

// retainedLog keeps draining a guest after the retained diagnostic prefix is
// full. The existing file size is included so a node-service restart cannot
// reset the limit.
type retainedLog struct {
	file      io.Writer
	remaining int64
}

func newRetainedLog(file *os.File, limit int64) (*retainedLog, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if limit > 0 && info.Size() > limit {
		if err = file.Truncate(limit); err != nil {
			return nil, err
		}
		info, err = file.Stat()
		if err != nil {
			return nil, err
		}
	}
	remaining := int64(0)
	if limit > 0 {
		remaining = max(0, limit-info.Size())
	}
	return &retainedLog{file: file, remaining: remaining}, nil
}

func enforceOutputRetention(dir string, limit int64) error {
	log, err := os.OpenFile(filepath.Join(dir, "console.log"), os.O_APPEND|os.O_WRONLY|syscall.O_NOFOLLOW, 0)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()
	info, err := log.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("worker console log is not a regular file")
	}
	_, err = newRetainedLog(log, limit)
	return err
}

func (w *retainedLog) Write(p []byte) (int, error) {
	keep := min(int64(len(p)), w.remaining)
	if keep > 0 {
		written, err := w.file.Write(p[:keep])
		w.remaining -= int64(written)
		if err != nil {
			return written, err
		}
	}
	return len(p), nil
}

func (t *terminalIO) Cancel()      { t.once.Do(func() { close(t.stop) }); <-t.done; t.loggingIO.Cancel() }
func (t *terminalIO) Close() error { t.Cancel(); return t.loggingIO.Close() }
func (t *terminalIO) Wait()        { t.Cancel() }
