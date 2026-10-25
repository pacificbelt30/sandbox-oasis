package authproxy

import (
	"context"
	"io"
	"log"
	"net/http"

	"github.com/pacificbelt30/codex-dock/internal/networkpolicy"
)

// workerFacingHandler returns the HTTP handler installed on the worker-facing
// listener. Behaviour depends on the role:
//
//   - RoleEgress: CONNECT tunnels and absolute-form HTTP requests (sent by clients
//     honoring HTTP(S)_PROXY) are handled by the forward proxy; everything else
//     falls through to mux (just /health).
//   - RoleAuth: this instance does NOT forward general traffic. CONNECT and
//     absolute-form requests are rejected (405); origin-form requests reach the
//     reverse-proxy / token mux.
func (p *Proxy) workerFacingHandler(mux http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p.WorkerTransportEnabled() && r.TLS != nil && len(r.TLS.VerifiedChains) > 0 && len(r.TLS.VerifiedChains[0]) > 0 {
			ctx, cancel := context.WithDeadline(r.Context(), r.TLS.VerifiedChains[0][0].NotAfter)
			defer cancel()
			r = r.WithContext(ctx)
		}
		isForward := r.Method == http.MethodConnect || (r.URL.IsAbs() && r.URL.Host != "")
		if isForward {
			if !p.cfg.isEgress() {
				http.Error(w, "this proxy does not forward general traffic; use the egress proxy", http.StatusMethodNotAllowed)
				return
			}
			if r.Method == http.MethodConnect {
				p.handleForwardConnect(w, r)
			} else {
				p.handleForwardHTTP(w, r)
			}
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// handleForwardConnect tunnels an HTTPS (or any TCP) connection to the requested
// host after an allowlist check. The proxy never sees the tunneled bytes (opaque
// TLS), which is why credential-injecting API traffic uses the reverse-proxy
// routes instead; this path carries general egress (git, npm, pip, curl, ...).
func (p *Proxy) handleForwardConnect(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	policy, err := p.forwardPolicy()
	if err != nil {
		http.Error(w, "invalid egress policy", http.StatusInternalServerError)
		return
	}
	destConn, err := (networkpolicy.Dialer{Policy: policy, Connect: p.connectVerified}).DialContext(r.Context(), "tcp", host)
	if err != nil {
		if isBlockedDestErr(err) {
			p.recordDenial(w, r, err)
			http.Error(w, err.Error(), http.StatusForbidden)
		} else {
			http.Error(w, err.Error(), http.StatusBadGateway)
		}
		return
	}

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "forward proxy requires HTTP/1.1", http.StatusInternalServerError)
		_ = destConn.Close()
		return
	}
	clientConn, buffered, err := hj.Hijack()
	if err != nil {
		log.Printf("forward proxy hijack error: %v", err)
		_ = destConn.Close()
		return
	}

	if _, err := clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		_ = clientConn.Close()
		_ = destConn.Close()
		return
	}

	// Splice the two connections together until either side closes.
	go func() {
		_, _ = io.Copy(destConn, buffered)
		_ = destConn.Close()
		_ = clientConn.Close()
	}()
	go func() {
		_, _ = io.Copy(clientConn, destConn)
		_ = clientConn.Close()
		_ = destConn.Close()
	}()
}

// handleForwardHTTP forwards a plain (non-CONNECT) HTTP request received in
// absolute form to its upstream and streams the response back to the worker.
func (p *Proxy) handleForwardHTTP(w http.ResponseWriter, r *http.Request) {
	policy, err := p.forwardPolicy()
	if err != nil {
		http.Error(w, "invalid egress policy", http.StatusInternalServerError)
		return
	}
	outReq := r.Clone(r.Context())
	if !enableStreamingResponse(w) {
		return
	}
	outReq.RequestURI = ""
	outReq.Host = outReq.URL.Host
	outReq.Header.Del("Proxy-Connection")
	outReq.Header.Del("Proxy-Authorization")
	client := *p.httpClient
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	// http.Client invokes this transport for every redirect hop as well.
	client.Transport = policyTransport{base: base, policy: policy}
	resp, err := client.Do(outReq)
	if err != nil {
		if isBlockedDestErr(err) {
			p.recordDenial(w, r, err)
			http.Error(w, err.Error(), http.StatusForbidden)
		} else {
			http.Error(w, err.Error(), http.StatusBadGateway)
		}
		return
	}
	defer func() { _ = resp.Body.Close() }()

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		panic(http.ErrAbortHandler)
	}
}
