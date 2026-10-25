package sandbox

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"github.com/pacificbelt30/codex-dock/internal/netquota"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const previewRelay = `
import socket,sys,threading
s=socket.create_connection(('127.0.0.1',int(sys.argv[1])),timeout=10);s.settimeout(None)
def upload():
    try:
        while True:
            data=sys.stdin.buffer.read1(65536)
            if not data:break
            s.sendall(data)
    except OSError:pass
    finally:
        try:s.shutdown(socket.SHUT_WR)
        except OSError:pass
threading.Thread(target=upload,daemon=True).start()
while True:
    data=s.recv(65536)
    if not data:break
    sys.stdout.buffer.write(data);sys.stdout.buffer.flush()
`

type Preview struct {
	URL    string
	cancel context.CancelFunc
	done   chan struct{}
}

func (p *Preview) Done() <-chan struct{} { return p.done }
func (p *Preview) Close() error          { p.cancel(); <-p.done; return nil }

// Preview serves one explicitly selected guest-loopback port. It uses verified
// exec streams, never a guest route to host management or a host TCP target.
func (m *Manager) Preview(ctx context.Context, worker string, port int, lifetime time.Duration) (*Preview, error) {
	if port < 1 || port > 65535 || lifetime <= 0 || lifetime > time.Hour {
		return nil, fmt.Errorf("preview requires a guest port and a lifetime up to one hour")
	}
	id, err := m.verifyExecWorker(ctx, worker)
	if err != nil {
		return nil, err
	}
	worker = id
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	limited, err := netquota.Wrap(ln, netquota.Limits{MaxConnections: 16, MaxConnectionsPerSource: 16})
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	ln = limited
	ctx, cancel := context.WithTimeout(ctx, lifetime)
	random := make([]byte, 32)
	if _, err = rand.Read(random); err != nil {
		cancel()
		_ = ln.Close()
		return nil, err
	}
	token := hex.EncodeToString(random)
	cookieName := "oaiso_preview_" + token[:12]
	authority := ln.Addr().String()
	origin := "http://" + authority
	var connections sync.Map
	slots := make(chan struct{}, 8)
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, ResponseHeaderTimeout: 30 * time.Second, DialContext: func(dialCtx context.Context, _, _ string) (net.Conn, error) {
		// The command lifetime follows the preview, not DialContext: Transport
		// cancels the latter immediately after a successful dial.
		if err := dialCtx.Err(); err != nil {
			return nil, err
		}
		a, b := net.Pipe()
		connections.Store(a, b)
		go func() {
			defer connections.Delete(a)
			defer func() { _ = a.Close() }()
			defer func() { _ = b.Close() }()
			_, _ = m.Exec(ctx, worker, []string{"python3", "-I", "-c", previewRelay, strconv.Itoa(port)}, b, b, io.Discard)
		}()
		return a, nil
	}}
	target := &url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(port)}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = transport
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "guest preview unavailable", http.StatusBadGateway)
	}
	proxy.ModifyResponse = func(r *http.Response) error {
		values := r.Header.Values("Set-Cookie")
		r.Header.Del("Set-Cookie")
		for _, value := range values {
			if !strings.HasPrefix(value, "oaiso_preview_") {
				r.Header.Add("Set-Cookie", value)
			}
		}
		return nil
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.Host != authority || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != origin) {
			http.Error(w, "preview origin refused", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/__oaiso_login" {
			if r.Method != "GET" || subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("token")), []byte(token)) != 1 {
				http.Error(w, "preview authorization required", http.StatusForbidden)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: int(lifetime.Seconds())})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		cookie, err := r.Cookie(cookieName)
		if err != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(token)) != 1 {
			http.Error(w, "preview authorization required", http.StatusForbidden)
			return
		}
		if _, err = m.verifyExecWorker(r.Context(), worker); err != nil {
			http.Error(w, "worker unavailable", http.StatusGone)
			cancel()
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			http.Error(w, "preview connection limit", http.StatusServiceUnavailable)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
		cookies := r.Cookies()
		r.Header.Del("Cookie")
		for _, c := range cookies {
			if !strings.HasPrefix(c.Name, "oaiso_preview_") {
				r.AddCookie(c)
			}
		}
		r.Header.Del("Forwarded")
		r.Header.Del("X-Forwarded-For")
		r.Header.Del("X-Forwarded-Host")
		r.Header.Del("X-Forwarded-Proto")
		proxy.ServeHTTP(w, r)
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, MaxHeaderBytes: 32 << 10}
	p := &Preview{URL: origin + "/__oaiso_login?token=" + token, cancel: cancel, done: make(chan struct{})}
	go func() { _ = server.Serve(ln); cancel() }()
	go func() {
		defer close(p.done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				_ = ln.Close()
				_ = server.Close()
				transport.CloseIdleConnections()
				connections.Range(func(a, b any) bool {
					first, ok := a.(net.Conn)
					if ok {
						_ = first.Close()
					}
					second, ok := b.(net.Conn)
					if ok {
						_ = second.Close()
					}
					return true
				})
				return
			case <-ticker.C:
				checkCtx, stopCheck := context.WithTimeout(ctx, 3*time.Second)
				observed, err := m.cli.ContainerInspect(checkCtx, worker)
				stopCheck()
				if err != nil || observed.State == nil || !observed.State.Running || observed.Config == nil || observed.Config.Labels["codex-dock.quarantined"] == "true" {
					cancel()
				}
			}
		}
	}()
	return p, nil
}
