package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// localTerminal owns the host-side terminal mode for an interactive exec.
// Polling instead of a blocking io.Copy lets a suspended TUI reclaim stdin
// immediately after the guest shell exits.
type localTerminal struct {
	fd       int
	previous *term.State
	resize   chan os.Signal
	done     chan struct{}
	once     sync.Once
	closeErr error
}

func openLocalTerminal(input io.Reader, tty bool, resize func(uint, uint) error) (*localTerminal, error) {
	if !tty {
		return nil, nil
	}
	file, ok := input.(*os.File)
	if !ok {
		return nil, nil
	}
	fd := int(file.Fd())
	if !term.IsTerminal(fd) {
		return nil, nil
	}
	previous, err := term.MakeRaw(fd)
	if err != nil {
		return nil, fmt.Errorf("setting local terminal raw mode: %w", err)
	}
	session := &localTerminal{fd: fd, previous: previous, resize: make(chan os.Signal, 1), done: make(chan struct{})}
	session.sendSize(resize)
	signal.Notify(session.resize, syscall.SIGWINCH)
	go func() {
		for {
			select {
			case <-session.done:
				return
			case <-session.resize:
				session.sendSize(resize)
			}
		}
	}()
	return session, nil
}

func (t *localTerminal) sendSize(resize func(uint, uint) error) {
	if resize == nil {
		return
	}
	width, height, err := term.GetSize(t.fd)
	if err == nil && width > 0 && height > 0 {
		_ = resize(uint(width), uint(height))
	}
}

func (t *localTerminal) copyTo(ctx context.Context, destination io.Writer) error {
	buffer := make([]byte, 32<<10)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.done:
			return nil
		default:
		}
		poll := []unix.PollFd{{Fd: int32(t.fd), Events: unix.POLLIN | unix.POLLHUP | unix.POLLERR}}
		_, err := unix.Poll(poll, 100)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if poll[0].Revents&(unix.POLLHUP|unix.POLLERR|unix.POLLNVAL) != 0 {
			return io.EOF
		}
		if poll[0].Revents&unix.POLLIN == 0 {
			continue
		}
		count, err := unix.Read(t.fd, buffer)
		if count > 0 {
			written := 0
			for written < count {
				n, writeErr := destination.Write(buffer[written:count])
				written += n
				if writeErr != nil {
					return writeErr
				}
				if n == 0 {
					return io.ErrShortWrite
				}
			}
		}
		if err != nil {
			return err
		}
	}
}

func (t *localTerminal) Close() error {
	t.once.Do(func() {
		signal.Stop(t.resize)
		close(t.done)
		t.closeErr = term.Restore(t.fd, t.previous)
	})
	return t.closeErr
}
