//go:build linux

package engine

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	cd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
	"golang.org/x/sys/unix"
)

// The shim keeps guest descriptors alive. Primary Kata terminals use a
// recoverable node-side FIFO collector; other tasks use the shim's logger.
type hub struct {
	mu     sync.Mutex
	io     cio.IO
	closed bool
	exit   *taskExit
}

func newHub() (*hub, error) { return &hub{}, nil }
func (h *hub) Close() error { h.mu.Lock(); h.closed = true; h.mu.Unlock(); return nil }
func hijack(w http.ResponseWriter) (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("streaming is unavailable")
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return nil, nil, err
	}
	_, err = rw.WriteString("HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.raw-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
	if err == nil {
		err = rw.Flush()
	}
	return conn, rw, err
}

func (b *Backend) streamHTTP(w http.ResponseWriter, r *http.Request, ctx context.Context, value, action string) {
	b.mu.Lock()
	rec, c, err := b.load(ctx, value)
	if err != nil {
		b.mu.Unlock()
		failure(w, err)
		return
	}
	if action == "jobs" {
		jobs := []container.ExecInspect{}
		for eid, e := range b.execs {
			if e.Container == rec.ID && e.Detached {
				jobs = append(jobs, container.ExecInspect{ExecID: eid, ContainerID: rec.ID, Running: e.Running, ExitCode: e.ExitCode})
			}
		}
		b.mu.Unlock()
		reply(w, jobs)
		return
	}
	if action == "exec" {
		if err = executionAllowed(rec); err != nil {
			b.mu.Unlock()
			failure(w, err)
			return
		}
		var cfg container.ExecOptions
		err = decode(r, &cfg)
		if err == nil && (cfg.Privileged || cfg.User != "" || len(cfg.Cmd) == 0) {
			err = fmt.Errorf("exec cannot change the worker's privilege or identity")
		}
		if err != nil {
			b.mu.Unlock()
			failure(w, err)
			return
		}
		eid, e := id()
		if e == nil {
			if len(b.execs) >= 4096 {
				e = fmt.Errorf("too many retained exec sessions")
			} else {
				rec.LastActivity = time.Now().UTC()
				e = b.save(rec)
				if e == nil {
					b.execs[eid] = &execution{ID: eid, Container: rec.ID, Config: cfg}
				}
			}
		}
		b.mu.Unlock()
		if e != nil {
			failure(w, e)
			return
		}
		time.AfterFunc(10*time.Minute, func() {
			b.mu.Lock()
			if e := b.execs[eid]; e != nil && !e.Started {
				delete(b.execs, eid)
			}
			b.mu.Unlock()
		})
		reply(w, map[string]string{"Id": eid})
		return
	}
	task, taskErr := c.Task(ctx, nil)
	b.mu.Unlock()
	if action == "logs" {
		b.logs(w, r, rec)
		return
	}
	if taskErr != nil && action == "wait" && rec.FinishedAt != "" {
		reply(w, container.WaitResponse{StatusCode: int64(rec.ExitCode)})
		return
	}
	wantOutput := r.URL.Query().Get("stdout") == "1" || r.URL.Query().Get("stderr") == "1"
	if action == "attach" && rec.FinishedAt != "" && r.URL.Query().Get("stdin") == "1" && !wantOutput {
		failure(w, fmt.Errorf("cannot attach stdin to an exited task"))
		return
	}
	if taskErr != nil && (action != "attach" || rec.FinishedAt == "" || !wantOutput) {
		failure(w, taskErr)
		return
	}
	if action == "wait" {
		wait, e := task.Wait(ctx)
		if e != nil {
			failure(w, e)
			return
		}
		select {
		case result := <-wait:
			code, _, e := result.Result()
			if e != nil {
				failure(w, e)
				return
			}
			reply(w, container.WaitResponse{StatusCode: int64(code)})
		case <-ctx.Done():
			return
		}
		return
	}
	conn, rw, err := hijack(w)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Time{})
	if taskErr != nil {
		_ = b.copyConsole(ctx, conn, rec.ID, 200, false)
		return
	}
	b.attachIO(ctx, conn, rw, rec, r.URL.Query().Get("stdin") == "1", wantOutput)
}

func (b *Backend) attachIO(ctx context.Context, conn net.Conn, reader io.Reader, rec *record, stdin, output bool) {
	if rec.Config.OpenStdin && stdin {
		// A fast process can exit before attach. Opening write-only would
		// block forever once its shim has closed the FIFO reader.
		input, err := os.OpenFile(filepath.Join(b.path(rec.ID), "stdin.fifo"), os.O_RDWR|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return
		}
		defer func() { _ = input.Close() }()
		if !output {
			// A restarted task has old console output. Replaying it to a
			// stdin-only client can fail as that client closes, closing the
			// FIFO before the input goroutine delivers its complete key frame.
			// Finish the requested input stream before releasing its writer.
			_, _ = io.Copy(input, reader)
			return
		}
		go func() { _, _ = io.Copy(input, reader) }()
	}
	_ = b.copyConsole(ctx, conn, rec.ID, 200, true)
}

type lockedWriter struct {
	mu     *sync.Mutex
	writer io.Writer
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(p)
}

// HTTP cancels the request context on stdin's half-close. Exec must keep
// producing output after EOF, but still kill a silent command on disconnect.
// The node serves Unix sockets: POLLHUP distinguishes full peer closure from
// a read-side EOF (POLLRDHUP).
func execStreamContext(parent context.Context, conn net.Conn) (context.Context, context.CancelFunc, error) {
	fdConn, ok := conn.(syscall.Conn)
	if !ok {
		return nil, nil, fmt.Errorf("exec requires a pollable node connection")
	}
	raw, err := fdConn.SyscallConn()
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				closed := false
				err := raw.Control(func(fd uintptr) {
					fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLHUP | unix.POLLERR}}
					_, err := unix.Poll(fds, 0)
					closed = (err != nil && err != unix.EINTR) || fds[0].Revents&(unix.POLLHUP|unix.POLLERR|unix.POLLNVAL) != 0
				})
				if err != nil || closed {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, cancel, nil
}

func (b *Backend) execHTTP(w http.ResponseWriter, r *http.Request, ctx context.Context, path string) {
	parts := strings.Split(strings.TrimPrefix(path, "/exec/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	eid := parts[0]
	b.mu.Lock()
	e := b.execs[eid]
	if e == nil {
		b.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	cfg := e.Config
	if parts[1] == "logs" || parts[1] == "kill" || parts[1] == "resize" {
		b.mu.Unlock()
		b.jobAction(w, r, ctx, eid, parts[1])
		return
	}
	if parts[1] == "json" {
		reply(w, container.ExecInspect{ExecID: eid, ContainerID: e.Container, Running: e.Running, ExitCode: e.ExitCode})
		b.mu.Unlock()
		return
	}
	if parts[1] != "start" {
		b.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	if e.Started {
		b.mu.Unlock()
		failure(w, fmt.Errorf("exec already started"))
		return
	}
	rec, c, err := b.load(ctx, e.Container)
	if err == nil {
		err = executionAllowed(rec)
	}
	if err != nil {
		b.mu.Unlock()
		failure(w, err)
		return
	}
	spec, err := c.Spec(ctx)
	if err != nil {
		b.mu.Unlock()
		failure(w, err)
		return
	}
	t, err := c.Task(ctx, nil)
	if err != nil {
		b.mu.Unlock()
		failure(w, err)
		return
	}
	var startConfig container.ExecStartOptions
	if err = decode(r, &startConfig); err != nil {
		b.mu.Unlock()
		failure(w, err)
		return
	}
	if startConfig.Detach {
		e.Started = true
		e.Running = true
		e.Detached = true
		err = b.startJob(ctx, e, t, spec.Process)
		if err != nil {
			e.Running = false
			e.ExitCode = 125
		}
		b.mu.Unlock()
		if err != nil {
			failure(w, err)
		} else {
			reply(w, map[string]string{"Id": eid})
		}
		return
	}
	execCtx, cancelExec := context.WithCancel(context.WithoutCancel(ctx))
	e.cancel = cancelExec
	e.done = make(chan struct{})
	e.Running = true
	e.Started = true
	b.mu.Unlock()
	defer cancelExec()
	defer close(e.done)
	ctx = execCtx
	conn, rw, err := hijack(w)
	if err != nil {
		b.mu.Lock()
		e.Running = false
		e.ExitCode = 125
		b.mu.Unlock()
		return
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Time{})
	if !cfg.AttachStdin {
		// Unused input may reach EOF while the client still reads output.
		// execStreamContext handles full disconnects for both stdin modes.
		go func() { _, _ = io.Copy(io.Discard, rw) }()
	}
	var mu sync.Mutex
	out := io.Writer(conn)
	stderr := io.Writer(conn)
	if !cfg.Tty {
		out = lockedWriter{&mu, stdcopy.NewStdWriter(conn, stdcopy.Stdout)}
		stderr = lockedWriter{&mu, stdcopy.NewStdWriter(conn, stdcopy.Stderr)}
	}
	out = cancelWriter{Writer: out, cancel: cancelExec}
	stderr = cancelWriter{Writer: stderr, cancel: cancelExec}
	processSpec := *spec.Process
	processSpec.Args = cfg.Cmd
	processSpec.Terminal = cfg.Tty
	processSpec.Env = append(append([]string{}, processSpec.Env...), cfg.Env...)
	if cfg.WorkingDir != "" {
		processSpec.Cwd = cfg.WorkingDir
	}
	var input io.Reader
	var eof *execInputEOF
	if !cfg.AttachStdin && !cfg.Tty {
		eof = newExecInputEOF()
		input = eof
		defer eof.Close()
	}
	if cfg.AttachStdin {
		input = rw
	}
	streamCtx, stop, err := execStreamContext(ctx, conn)
	var p cd.Process
	if err == nil {
		defer stop()
		stopCancel := context.AfterFunc(execCtx, stop)
		defer stopCancel()
		ctx = streamCtx
		creator := cio.NewCreator(cio.WithStreams(input, out, stderr), func(s *cio.Streams) { s.Terminal = cfg.Tty })
		p, err = t.Exec(ctx, eid, &processSpec, creator)
	}
	exit := 125
	if err == nil {
		var wait <-chan cd.ExitStatus
		wait, err = p.Wait(ctx)
		if err == nil {
			err = p.Start(ctx)
			if err == nil && eof != nil {
				eof.Close()
			}
		}
		if err == nil && !cfg.AttachStdin && !cfg.Tty {
			// Kata may keep an empty stdin pipe open even when no FIFO was
			// supplied. Explicit EOF prevents noninteractive CLIs waiting for
			// additional input forever. Attached streams retain their own EOF.
			err = p.CloseIO(ctx, cd.WithStdinCloser)
			if errdefs.IsNotFound(err) {
				err = nil
			}
		}
		if err == nil {
			select {
			case status := <-wait:
				code, _, e := status.Result()
				err = e
				exit = int(code)
			case <-ctx.Done():
				err = ctx.Err()
				_ = p.Kill(context.WithoutCancel(ctx), 9)
			}
		}
		_, _ = p.Delete(context.WithoutCancel(ctx), cd.WithProcessKill)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
	}
	b.mu.Lock()
	e.Running = false
	e.ExitCode = exit
	e.cancel = nil
	if rec, _, loadErr := b.load(context.WithoutCancel(ctx), e.Container); loadErr == nil {
		rec.LastActivity = time.Now().UTC()
		_ = b.save(rec)
	}
	b.mu.Unlock()
	time.AfterFunc(10*time.Minute, func() { b.mu.Lock(); delete(b.execs, eid); b.mu.Unlock() })
}

func (b *Backend) logs(w http.ResponseWriter, r *http.Request, rec *record) {
	tail, err := strconv.Atoi(r.URL.Query().Get("tail"))
	if err != nil {
		tail = -1
	}
	w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
	if err = b.copyConsole(r.Context(), w, rec.ID, tail, r.URL.Query().Get("follow") == "1"); err != nil {
		return
	}
}
func (b *Backend) copyConsole(ctx context.Context, w io.Writer, cid string, tail int, follow bool) error {
	f, err := os.Open(filepath.Join(b.path(cid), "console.log"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if tail >= 0 {
		if err = seekTail(f, tail); err != nil {
			return err
		}
	}
	for {
		if _, err = io.Copy(w, f); err != nil {
			return err
		}
		if flush, ok := w.(http.Flusher); ok {
			flush.Flush()
		}
		if !follow {
			return nil
		}
		b.mu.Lock()
		h := b.hubs[cid]
		running := false
		if h != nil {
			h.mu.Lock()
			running = !h.closed
			h.mu.Unlock()
		}
		b.mu.Unlock()
		if !running {
			// Exit may race the preceding EOF; collectors drain before closing
			// the hub. Read again after observing that closure.
			_, err = io.Copy(w, f)
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func seekTail(f *os.File, n int) error {
	end, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	pos := end
	remaining := n
	buf := make([]byte, 32768)
	for pos > 0 {
		size := int64(len(buf))
		if pos < size {
			size = pos
		}
		pos -= size
		chunk := buf[:size]
		if _, err = f.ReadAt(chunk, pos); err != nil {
			return err
		}
		for i := len(chunk) - 1; i >= 0; i-- {
			if chunk[i] == '\n' && pos+int64(i) != end-1 {
				remaining--
				if remaining == 0 {
					_, err = f.Seek(pos+int64(i)+1, io.SeekStart)
					return err
				}
			}
		}
	}
	_, err = f.Seek(0, io.SeekStart)
	return err
}

type cancelWriter struct {
	io.Writer
	cancel context.CancelFunc
}

func (w cancelWriter) Write(data []byte) (int, error) {
	n, err := w.Writer.Write(data)
	if err != nil {
		w.cancel()
	}
	return n, err
}
