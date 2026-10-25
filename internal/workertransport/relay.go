package workertransport

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

// Relay listens only on VM loopback. All worker-network traffic uses TLS 1.3.
// Go ReverseProxy preserves HTTP/SSE and upgrades without provider-specific code.
func Relay(ctx context.Context, c Config, ready func()) error {
	tlsConfig, e := c.TLSConfig()
	if e != nil {
		return e
	}
	endpoint, _ := url.Parse(c.Endpoint)
	transport := &http.Transport{TLSClientConfig: tlsConfig, Proxy: nil, MaxIdleConns: 32, MaxIdleConnsPerHost: 16, IdleConnTimeout: 30 * time.Second, TLSHandshakeTimeout: 10 * time.Second, ForceAttemptHTTP2: false}
	defer transport.CloseIdleConnections()
	proxy := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		r.SetURL(endpoint)
		r.Out.Host = endpoint.Host
		r.Out.Header.Del("Forwarded")
		r.Out.Header.Del("X-Forwarded-For")
		r.Out.Header.Del("X-Forwarded-Host")
		r.Out.Header.Del("X-Forwarded-Proto")
	}, Transport: transport, FlushInterval: -1, ErrorLog: log.New(io.Discard, "", 0), ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "worker TLS connection unavailable", http.StatusBadGateway)
	}}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect || r.URL.IsAbs() {
			http.Error(w, "origin requests required", 400)
			return
		}
		proxy.ServeHTTP(w, r)
	})
	ln, e := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:18083")
	if e != nil {
		return e
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second, MaxHeaderBytes: 64 << 10}
	stop := context.AfterFunc(ctx, func() { _ = server.Close() })
	defer stop()
	if ready != nil {
		ready()
	}
	e = server.Serve(ln)
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}
