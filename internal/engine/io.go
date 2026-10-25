//go:build linux

package engine

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"

	"github.com/containerd/containerd/v2/pkg/cio"
)

const primaryOutputLimit int64 = 16 << 20

// Binary loggers own output independently of this service. Primary Kata
// terminals wrap this input state with a recoverable passfd FIFO collector.
type loggingIO struct {
	config    cio.Config
	eof       *os.File
	closeOnce sync.Once
	closeErr  error
}

func (l *loggingIO) CloseInput() error {
	l.closeOnce.Do(func() {
		if l.eof != nil {
			l.closeErr = l.eof.Close()
		}
	})
	return l.closeErr
}

func (l *loggingIO) Config() cio.Config { return l.config }
func (l *loggingIO) Cancel()            { _ = l.CloseInput() }
func (*loggingIO) Wait()                {}
func (l *loggingIO) Close() error       { return l.CloseInput() }
func (b *Backend) logIO(r *record) (cio.Creator, error) {
	creator, err := b.logIOAt(b.path(r.ID), r.ID, r.Config.Tty, r.Config.OpenStdin, primaryOutputLimit)
	if err != nil {
		return nil, err
	}
	if r.Host.Runtime != "runc" && r.Config.Tty {
		return func(id string) (cio.IO, error) {
			streams, err := creator(id)
			if err != nil {
				return nil, err
			}
			owned, ok := streams.(*loggingIO)
			if !ok {
				_ = streams.Close()
				return nil, fmt.Errorf("unexpected terminal I/O implementation")
			}
			terminal, err := openTerminalIO(b.path(r.ID), owned, primaryOutputLimit)
			if err != nil {
				_ = owned.Close()
				return nil, err
			}
			return terminal, nil
		}, nil
	}
	if r.Host.Runtime != "runc" || !r.Config.OpenStdin || r.Config.Tty {
		return creator, nil
	}
	// Runc's binary logging driver deliberately has no stdin. Its file driver
	// does connect the input FIFO, and recognizes a FIFO output path. Route that
	// output through the same bounded collector as Kata terminals instead of an
	// unbounded regular file.
	return func(id string) (cio.IO, error) {
		streams, err := creator(id)
		if err != nil {
			return nil, err
		}
		owned, ok := streams.(*loggingIO)
		if !ok {
			_ = streams.Close()
			return nil, fmt.Errorf("unexpected stdin I/O implementation")
		}
		output, err := openRuncStdinIO(b.path(r.ID), owned, primaryOutputLimit)
		if err != nil {
			_ = owned.Close()
			return nil, err
		}
		return output, nil
	}, nil
}
func (b *Backend) logIOAt(dir, id string, tty, stdin bool, limit int64) (cio.Creator, error) {
	input := ""
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if stdin {
		input = filepath.Join(dir, "stdin.fifo")
		if info, err := os.Lstat(input); err == nil {
			if info.Mode()&os.ModeNamedPipe == 0 {
				return nil, fmt.Errorf("stdin path is not a FIFO")
			}
		} else if os.IsNotExist(err) {
			if err = syscall.Mkfifo(input, 0600); err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}
	u := &url.URL{Scheme: "binary", Path: b.opts.Logger}
	q := url.Values{}
	q.Set("--state", dir)
	q.Set("--id", id)
	if limit > 0 {
		q.Set("--max-bytes", strconv.FormatInt(limit, 10))
	}
	u.RawQuery = q.Encode()
	return func(string) (cio.IO, error) {
		l := &loggingIO{config: cio.Config{Stdin: input, Stdout: u.String(), Stderr: u.String(), Terminal: tty}}
		if stdin {
			// Hold a writer before NewTask: the process can read stdin before the
			// binary logger has started its independent keepalive. Without this,
			// early readers can observe EOF and exit before the first attach.
			f, err := os.OpenFile(input, os.O_RDWR|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
			if err != nil {
				return nil, err
			}
			l.eof = f
		}
		if !stdin && !tty {
			// A real FIFO is required by Kata passfd I/O. Keep a writer until
			// Start, then close it to deliver EOF. The shim logger only keeps
			// interactive stdin.fifo open; this pipe needs no keeper on restart.
			path := filepath.Join(dir, "stdin-eof.fifo")
			if info, err := os.Lstat(path); err == nil {
				if info.Mode()&os.ModeNamedPipe == 0 {
					return nil, fmt.Errorf("EOF stdin path is not a FIFO")
				}
			} else if os.IsNotExist(err) {
				if err := syscall.Mkfifo(path, 0600); err != nil {
					return nil, err
				}
			} else {
				return nil, err
			}
			f, err := os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
			if err != nil {
				return nil, err
			}
			l.eof = f
			l.config.Stdin = path
		}
		return l, nil
	}, nil
}

// Keep the FIFO writer alive through passfd initialization. An immediately
// empty reader can close it before Kata opens its peer, making Start fail.
type execInputEOF struct {
	done chan struct{}
	once sync.Once
}

func newExecInputEOF() *execInputEOF             { return &execInputEOF{done: make(chan struct{})} }
func (e *execInputEOF) Read([]byte) (int, error) { <-e.done; return 0, io.EOF }
func (e *execInputEOF) Close()                   { e.once.Do(func() { close(e.done) }) }
