package authproxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// The real destination policy and verified-IP dialer remain in use. Only the
// trusted upstream chain is synthetic, so no Internet service is required.
func runKataEgressFixture(cfg Config) int {
	connections, closeObserver, err := startKataConnectionObserver()
	if err != nil {
		return 1
	}
	defer closeObserver()
	chain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != "203.0.113.1:443" && r.Host != "203.0.113.1:80" && r.Host != "[2606:4700:4700::1111]:443" {
			http.Error(w, "unexpected verified destination", http.StatusForbidden)
			return
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		defer connections.track()()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Minute))
		if _, err = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		if err = rw.Flush(); err != nil {
			return
		}
		if strings.HasSuffix(r.Host, ":443") {
			_, _ = io.Copy(conn, rw)
			return
		}
		req, err := http.ReadRequest(rw.Reader)
		if err != nil {
			return
		}
		_ = req.Body.Close()
		if req.URL.Path == "/redirect-metadata" {
			_, _ = io.WriteString(conn, "HTTP/1.1 302 Found\r\nLocation: http://169.254.169.254/latest/meta-data/\r\nContent-Length: 0\r\n\r\n")
			return
		}
		if _, err = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nContent-Length: 1000000\r\n\r\nready\n"+strings.Repeat("x", 65536)); err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, rw)
	}))
	defer chain.Close()
	cfg.UpstreamProxyURL = chain.URL
	p, err := NewProxy(cfg)
	if err != nil {
		return 1
	}
	if err = p.Start(); err != nil {
		return 1
	}
	defer p.Stop()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	<-ctx.Done()
	return 0
}
