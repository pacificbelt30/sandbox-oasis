package authproxy

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// reverseProxy strips stripPrefix from r.URL.Path, appends it to targetBase,
// and forwards the request upstream, copying status, headers, and body back.
// inject overwrites auth headers with the host's real credentials before the
// request leaves the proxy.
func (p *Proxy) reverseProxy(w http.ResponseWriter, r *http.Request, stripPrefix, targetBase string, inject func(http.Header)) {
	authenticated, done, ok := p.workerRequest(r, stripPrefix == "/anthropic")
	if !ok {
		http.Error(w, "worker token invalid or expired", http.StatusUnauthorized)
		return
	}
	defer done()
	r = authenticated
	if !p.authorizeModel(w, r) {
		return
	}
	w, r, finishUsage, permitted := p.meterUsage(w, r)
	if !permitted {
		return
	}
	defer finishUsage()
	if stripPrefix == "/anthropic" {
		if err := p.refreshAnthropicOAuthIfNeeded(r.Context()); err != nil {
			// Do not expose upstream response bodies, tokens or store errors.
			http.Error(w, "Anthropic credential refresh failed", http.StatusBadGateway)
			return
		}
	}
	// Preserve the encoded path so a newly added upstream endpoint can use its
	// own path syntax without the proxy decoding and re-encoding it.
	path := strings.TrimPrefix(r.URL.EscapedPath(), stripPrefix)
	target := targetBase + path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}

	// WebSocket upgrade requests require raw TCP tunneling, not HTTP proxying.
	if isWebSocketRequest(r) {
		p.handleWebSocketProxy(w, r, target, inject)
		return
	}

	// Forward the body as it arrives. Buffering it here delays streamed uploads
	// and makes the proxy depend on the request payload size and schema.
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		http.Error(w, "creating upstream request", http.StatusInternalServerError)
		return
	}
	req.ContentLength = r.ContentLength
	if !enableStreamingResponse(w) {
		return
	}

	// Copy headers, skipping hop-by-hop headers.
	hopByHop := map[string]bool{
		"connection":          true,
		"keep-alive":          true,
		"proxy-authenticate":  true,
		"proxy-authorization": true,
		"te":                  true,
		"trailers":            true,
		"transfer-encoding":   true,
		"upgrade":             true,
	}
	for k, vv := range r.Header {
		if hopByHop[strings.ToLower(k)] || strings.EqualFold(k, "X-Codex-Token") || strings.EqualFold(k, "Authorization") || strings.EqualFold(k, "X-Api-Key") {
			continue
		}
		for _, v := range vv {
			req.Header.Add(k, v)
		}
	}
	// Inject real host credentials, overriding any placeholder the container sent.
	if r.Context().Err() != nil {
		http.Error(w, "worker request canceled or expired", http.StatusUnauthorized)
		return
	}
	inject(req.Header)
	if budget, _ := r.Context().Value(usagePolicyKey{}).(*tokenUsage); budget != nil && budget.policy.MaxTokens > 0 {
		req.Header.Set("Accept-Encoding", "identity")
	}

	client := p.httpClient
	if budget, _ := r.Context().Value(usagePolicyKey{}).(*tokenUsage); p.modelPolicyActive(r) || budget != nil {
		copy := *client
		copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		client = &copy
	}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "upstream error: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if err := copyAndFlush(w, resp.Body); err != nil {
		// Headers are already sent. A normal return would emit a successful
		// chunk terminator and disguise a truncated upstream as clean EOF.
		panic(http.ErrAbortHandler)
	}

	if p.cfg.Verbose {
		fmt.Printf("Proxied %s %s -> %s (%d)\n", r.Method, r.URL.Path, target, resp.StatusCode)
	}
}

// The upstream transport owns the request body until its writer finishes,
// which can be after response headers arrive. HTTP/1's default response drain
// can otherwise read/close that same body and abort a healthy upstream stream.
func enableStreamingResponse(w http.ResponseWriter) bool {
	if err := http.NewResponseController(w).EnableFullDuplex(); err != nil && !errors.Is(err, http.ErrNotSupported) {
		http.Error(w, "enabling streamed response", http.StatusInternalServerError)
		return false
	}
	return true
}

// copyAndFlush streams src to w, flushing after every chunk when w supports
// http.Flusher. This preserves real-time delivery of Server-Sent Events
// (text/event-stream), which both the Anthropic Messages API and the OpenAI
// Responses API use for streaming responses. A plain io.Copy would let the
// net/http response buffer hold events until it fills or the handler returns,
// stalling interactive agents.
func copyAndFlush(w http.ResponseWriter, src io.Reader) error {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				return nil
			}
			return rerr
		}
	}
}
