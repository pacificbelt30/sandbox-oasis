package authproxy

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/netquota"
)

func TestLimitedCONNECTPreservesBufferedPayloadAndClosesOnStop(t *testing.T) {
	echo, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	peerClosed := make(chan struct{})
	go func() {
		c, err := echo.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = io.Copy(c, c)
		close(peerClosed)
	}()
	p, err := NewProxy(Config{Role: RoleEgress, NetworkResources: netquota.Limits{MaxConnections: 2, MaxConnectionsPerSource: 1, BytesPerSecond: 65536}})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	_, address, _ := strings.Cut(p.Endpoint(), "://")
	client, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))
	// Payload may already be buffered by the HTTP parser at Hijack time.
	_, err = fmt.Fprintf(client, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\nearly", echo.Addr(), echo.Addr())
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(client)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("CONNECT: %v %v", response, err)
	}
	defer func() { _ = client.Close(); _ = response.Body.Close() }()
	data := make([]byte, 5)
	if _, err := io.ReadFull(reader, data); err != nil || string(data) != "early" {
		t.Fatalf("buffered payload lost: %q %v", data, err)
	}
	p.Stop()
	if _, err := reader.ReadByte(); err == nil {
		t.Fatal("hijacked connection remained open after stop")
	}
	select {
	case <-peerClosed:
	case <-time.After(time.Second):
		t.Fatal("upstream tunnel retained after stop")
	}
}

func TestLimitedWebSocketPreservesBufferedClientPayload(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, err = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n"); err != nil {
			return
		}
		if err = rw.Flush(); err != nil {
			return
		}
		_, _ = io.Copy(conn, rw)
	}))
	defer upstream.Close()
	p := newTestProxy(t)
	p.cfg.NetworkResources = netquota.Limits{MaxConnections: 2, BytesPerSecond: 65536}
	p.apiUpstreamURL = upstream.URL
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	token, err := p.IssueToken("buffered-ws", 60)
	if err != nil {
		t.Fatal(err)
	}
	_, address, _ := strings.Cut(p.Endpoint(), "://")
	conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := fmt.Fprintf(conn, "GET /v1/responses HTTP/1.1\r\nHost: proxy\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nAuthorization: Bearer %s\r\n\r\nearly", token); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil || response.StatusCode != 101 {
		t.Fatalf("upgrade: %v %v", response, err)
	}
	defer func() { _ = conn.Close(); _ = response.Body.Close() }()
	payload := make([]byte, 5)
	if _, err := io.ReadFull(reader, payload); err != nil || string(payload) != "early" {
		t.Fatalf("buffered WebSocket data lost: %q %v", payload, err)
	}
}
