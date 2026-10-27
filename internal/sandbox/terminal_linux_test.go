package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

func testLocalPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = master.Close() })
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = slave.Close() })
	return master, slave
}

func TestLocalTerminalRawKeysRestoreAndReclaimInput(t *testing.T) {
	master, slave := testLocalPTY(t)
	fd := int(slave.Fd())
	before, err := term.GetState(fd)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Col: 91, Row: 33}); err != nil {
		t.Fatal(err)
	}
	sizes := make(chan [2]uint, 8)
	session, err := openLocalTerminal(slave, true, func(w, h uint) error { sizes <- [2]uint{w, h}; return nil })
	if err != nil || session == nil {
		t.Fatal(err)
	}
	defer session.Close()
	if size := <-sizes; size != [2]uint{91, 33} {
		t.Fatal(size)
	}
	output := terminalTestWriter{chunks: make(chan []byte, 8)}
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { done <- session.copyTo(ctx, output) }()
	payload := []byte("\x1b\x1b[A\x03\x1a\t\x1b[200~日本語\n貼り付け\x1b[201~")
	if _, err := master.Write(payload); err != nil {
		t.Fatal(err)
	}
	var received []byte
	timeout := time.After(2 * time.Second)
	for len(received) < len(payload) {
		select {
		case chunk := <-output.chunks:
			received = append(received, chunk...)
		case <-timeout:
			t.Fatal("keys were buffered until newline", received)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stdin reader did not stop on cancellation")
	}
	if !bytes.Equal(received, payload) {
		t.Fatalf("keys changed: %x; want %x", received, payload)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	after, _ := term.GetState(fd)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("terminal flags were not restored")
	}
	if _, err := master.Write([]byte("next-ui-input\n")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 64)
	n, err := slave.Read(buffer)
	if err != nil || string(buffer[:n]) != "next-ui-input\n" {
		t.Fatal("ended session stole resumed input", n, err)
	}
}

type terminalTestWriter struct{ chunks chan []byte }

func (w terminalTestWriter) Write(data []byte) (int, error) {
	w.chunks <- append([]byte(nil), data...)
	return len(data), nil
}

func TestLocalTerminalResizeAndIdleClose(t *testing.T) {
	_, slave := testLocalPTY(t)
	sizes := make(chan [2]uint, 8)
	session, err := openLocalTerminal(slave, true, func(w, h uint) error { sizes <- [2]uint{w, h}; return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: 101, Row: 37}); err != nil {
		t.Fatal(err)
	}
	session.resize <- unix.SIGWINCH
	select {
	case size := <-sizes:
		if size != [2]uint{101, 37} {
			t.Fatal(size)
		}
	case <-time.After(time.Second):
		t.Fatal("resize lost")
	}
	done := make(chan error, 1)
	go func() { done <- session.copyTo(context.Background(), &bytes.Buffer{}) }()
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("idle reader leaked after terminal close")
	}
}
