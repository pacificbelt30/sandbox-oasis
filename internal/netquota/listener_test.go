package netquota

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func acceptPair(t *testing.T, l *Listener, source string) (net.Conn, *connection) {
	t.Helper()
	d := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(source)}, Timeout: time.Second}
	client, err := d.DialContext(t.Context(), "tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	server, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	return client, server.(*connection)
}

func testListener(t *testing.T, limits Limits) *Listener {
	t.Helper()
	raw, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l, err := Wrap(raw, limits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func TestAdmissionRetainsExistingConnectionsAndSeparatesSources(t *testing.T) {
	l := testListener(t, Limits{MaxConnections: 2, MaxConnectionsPerSource: 1})
	first, server := acceptPair(t, l, "127.0.0.2")
	d := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP("127.0.0.2")}}
	rejected, err := d.DialContext(t.Context(), "tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer rejected.Close()
	accepted := make(chan net.Conn, 1)
	go func() { c, _ := l.Accept(); accepted <- c }()
	_ = rejected.SetReadDeadline(time.Now().Add(time.Second))
	if _, err = rejected.Read(make([]byte, 1)); err == nil {
		t.Fatal("source exceeded its connection limit")
	} else {
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			t.Fatal("overflow socket was retained instead of closed")
		}
	}
	d.LocalAddr = &net.TCPAddr{IP: net.ParseIP("127.0.0.3")}
	other, err := d.DialContext(t.Context(), "tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	select {
	case c := <-accepted:
		if c == nil {
			t.Fatal("other source refused")
		}
		defer c.Close()
		if !strings.HasPrefix(c.RemoteAddr().String(), "127.0.0.3:") {
			t.Fatal("wrong source admitted")
		}
	case <-time.After(time.Second):
		t.Fatal("other source blocked")
	}
	if _, err = server.Write([]byte("alive")); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 5)
	if _, err = io.ReadFull(first, data); err != nil || string(data) != "alive" {
		t.Fatal("existing connection interrupted", err)
	}
	// A global overflow is also refused, and Close releases all live slots.
	go func() { _, _ = l.Accept() }()
	d.LocalAddr = &net.TCPAddr{IP: net.ParseIP("127.0.0.4")}
	extra, err := d.DialContext(t.Context(), "tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer extra.Close()
	_ = extra.SetReadDeadline(time.Now().Add(time.Second))
	if _, err = extra.Read(make([]byte, 1)); err == nil {
		t.Fatal("global overflow accepted")
	} else {
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			t.Fatal("global overflow socket was retained")
		}
	}
	_ = l.Close()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.conns) != 0 {
		t.Fatal("connections retained after close")
	}
}

func TestBandwidthCombinesDirectionsAndSurvivesReconnect(t *testing.T) {
	l := testListener(t, Limits{BytesPerSecond: 32 << 10})
	client, server := acceptPair(t, l, "127.0.0.2")
	// Exhaust the shared burst without depending on the scheduling of writes.
	server.peer.mu.Lock()
	server.peer.tokens = 0
	server.peer.last = time.Now()
	server.peer.mu.Unlock()
	start := time.Now()
	aggregateStart := start
	go func() { _, _ = client.Write(make([]byte, 8<<10)) }()
	if _, err := io.ReadFull(server, make([]byte, 8<<10)); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 180*time.Millisecond {
		t.Fatalf("ingress bypassed rate: %s", elapsed)
	}
	_ = server.Close()
	_ = client.Close()
	client2, server2 := acceptPair(t, l, "127.0.0.2")
	if server2.peer != server.peer {
		t.Fatal("reconnect reset source budget")
	}
	go func() { _, _ = io.Copy(io.Discard, client2) }()
	if _, err := server2.Write(make([]byte, 8<<10)); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(aggregateStart); elapsed < 400*time.Millisecond {
		t.Fatalf("egress bypassed shared rate: %s", elapsed)
	}
	// Another source starts with its own burst, unaffected by this source's debt.
	otherClient, independent := acceptPair(t, l, "127.0.0.3")
	go func() { _, _ = io.Copy(io.Discard, otherClient) }()
	if err := independent.SetWriteDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := independent.Write(make([]byte, burst)); err != nil {
		t.Fatal("another source inherited bandwidth debt:", err)
	}
}

func TestThrottleObservesChangedDeadlineAndClose(t *testing.T) {
	for _, closeInstead := range []bool{false, true} {
		l := testListener(t, Limits{BytesPerSecond: 1024})
		_, server := acceptPair(t, l, "127.0.0.2")
		server.peer.mu.Lock()
		server.peer.tokens = 0
		server.peer.last = time.Now()
		server.peer.mu.Unlock()
		done := make(chan error, 1)
		go func() { _, err := server.Write(make([]byte, burst)); done <- err }()
		if closeInstead {
			_ = server.Close()
		} else {
			_ = server.SetWriteDeadline(time.Now().Add(25 * time.Millisecond))
		}
		select {
		case err := <-done:
			if closeInstead && !errors.Is(err, net.ErrClosed) || !closeInstead && !errors.Is(err, os.ErrDeadlineExceeded) {
				t.Fatalf("wrong interruption: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("throttled connection cannot be interrupted")
		}
	}
}

func TestLimitValidation(t *testing.T) {
	for _, l := range []Limits{{MaxConnections: -1}, {MaxConnections: 65537}, {MaxConnectionsPerSource: -1}, {MaxConnections: 1, MaxConnectionsPerSource: 2}, {BytesPerSecond: 1}, {BytesPerSecond: -1}, {BytesPerSecond: 1<<40 + 1}} {
		if l.Validate() == nil {
			t.Fatalf("invalid limits accepted: %+v", l)
		}
	}
	if (Limits{}).ID() != "" || (Limits{}).Validate() != nil {
		t.Fatal("unlimited compatibility lost")
	}
}

type observedRead struct {
	net.Conn
	consumed chan struct{}
	proceed  <-chan struct{}
}

func (c observedRead) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		select {
		case c.consumed <- struct{}{}:
		default:
		}
	}
	<-c.proceed
	return n, err
}

func TestInterruptedThrottlePreservesAlreadyReadTCPBytes(t *testing.T) {
	l := testListener(t, Limits{BytesPerSecond: 1024})
	client, server := acceptPair(t, l, "127.0.0.2")
	consumed := make(chan struct{}, 1)
	proceed := make(chan struct{})
	server.Conn = observedRead{Conn: server.Conn, consumed: consumed, proceed: proceed}
	server.peer.mu.Lock()
	server.peer.tokens = 0
	server.peer.last = time.Now()
	server.peer.mu.Unlock()
	payload := bytes.Repeat([]byte("x"), 1024)
	result := make(chan error, 1)
	go func() { _, err := server.Read(make([]byte, len(payload))); result <- err }()
	if _, err := client.Write(payload); err != nil {
		t.Fatal(err)
	}
	select {
	case <-consumed:
	case <-time.After(time.Second):
		t.Fatal("raw data was not read")
	}
	if err := server.SetReadDeadline(time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	close(proceed)
	select {
	case err := <-result:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("expected temporary interruption: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("read cannot be interrupted")
	}
	if err := server.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	server.peer.mu.Lock()
	server.peer.tokens = burst
	server.peer.last = time.Now()
	server.peer.mu.Unlock()
	restored := make([]byte, len(payload))
	if _, err := io.ReadFull(server, restored); err != nil || !bytes.Equal(restored, payload) {
		t.Fatalf("TCP bytes lost across temporary deadline: %v", err)
	}
}
