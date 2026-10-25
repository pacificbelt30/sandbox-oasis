package authproxy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/pacificbelt30/codex-dock/internal/egresscontrol"
	"github.com/pacificbelt30/codex-dock/internal/networkpolicy"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLiveEgressClosesHTTPAndCONNECTPersistsAndRejectsStaleUpdates(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: ready\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer backend.Close()
	backendURL, _ := url.Parse(backend.URL)
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" {
			t.Error("upstream was not CONNECT")
			w.WriteHeader(400)
			return
		}
		dst, err := net.Dial("tcp", backendURL.Host)
		if err != nil {
			t.Error(err)
			return
		}
		src, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			_ = dst.Close()
			return
		}
		_, _ = io.WriteString(src, "HTTP/1.1 200 OK\r\n\r\n")
		go func() { _, _ = io.Copy(dst, buf); _ = dst.Close(); _ = src.Close() }()
		go func() { _, _ = io.Copy(src, dst); _ = dst.Close(); _ = src.Close() }()
	}))
	defer bridge.Close()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Role: RoleEgress, ListenAddr: "127.0.0.1:0", PolicyControlDir: dir, ForwardPolicy: networkpolicy.Rules{DefaultDeny: true, BlockPrivate: true, AllowCIDRs: []string{"8.8.8.8/32"}, AllowPorts: []int{80}}, UpstreamProxyURL: bridge.URL}
	p, err := NewProxy(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	endpoint, _ := url.Parse(p.Endpoint())
	transport := &http.Transport{Proxy: http.ProxyURL(endpoint)}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 4 * time.Second}
	resp, err := client.Get("http://8.8.8.8/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, len("data: ready\n\n"))
	if _, err = io.ReadFull(resp.Body, buf); err != nil {
		t.Fatal(err)
	}
	tunnel, err := net.Dial("tcp", endpoint.Host)
	if err != nil {
		t.Fatal(err)
	}
	defer tunnel.Close()
	_ = tunnel.SetDeadline(time.Now().Add(4 * time.Second))
	_, _ = fmt.Fprint(tunnel, "CONNECT 8.8.8.8:80 HTTP/1.1\r\nHost: 8.8.8.8:80\r\n\r\n")
	reader := bufio.NewReader(tunnel)
	connectResp, err := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
	if err != nil || connectResp.StatusCode != 200 {
		t.Fatal("CONNECT failed", err)
	}
	defer func() { _ = connectResp.Body.Close() }()
	_, _ = fmt.Fprint(tunnel, "GET /stream HTTP/1.1\r\nHost: 8.8.8.8\r\n\r\n")
	stream, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	if _, err = io.ReadFull(stream.Body, buf); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	state, err := egresscontrol.Call(ctx, dir, nil)
	if err != nil || state.Generation != 1 {
		t.Fatal(state, err)
	}
	denied := networkpolicy.Rules{DefaultDeny: true, BlockPrivate: true}
	state, err = egresscontrol.Call(ctx, dir, &egresscontrol.Update{ExpectedGeneration: 1, Rules: denied})
	if err != nil || !state.Ready || state.Generation != 2 {
		t.Fatal(state, err)
	}
	if _, err = resp.Body.Read(buf); err == nil {
		t.Fatal("HTTP stream survived policy update")
	}
	if _, err = stream.Body.Read(buf); err == nil {
		t.Fatal("CONNECT stream survived policy update")
	}
	blocked, err := client.Get("http://8.8.8.8/")
	if err != nil {
		t.Fatal(err)
	}
	_ = blocked.Body.Close()
	if blocked.StatusCode != 403 {
		t.Fatal("new connection was not blocked", blocked.StatusCode)
	}
	if _, err = egresscontrol.Call(ctx, dir, &egresscontrol.Update{ExpectedGeneration: 1, Rules: cfg.ForwardPolicy}); err == nil {
		t.Fatal("stale policy update accepted")
	}
	outcomes := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := egresscontrol.Call(ctx, dir, &egresscontrol.Update{ExpectedGeneration: 2, Rules: denied})
			outcomes <- err
		}()
	}
	successes := 0
	for i := 0; i < 2; i++ {
		if <-outcomes == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatal("concurrent generation updates both applied", successes)
	}
	p.Stop()
	restarted, err := NewProxy(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = restarted.Start(); err != nil {
		t.Fatal(err)
	}
	state, err = egresscontrol.Call(ctx, dir, nil)
	if err != nil || state.Generation != 3 || state.PolicyID != egresscontrol.ID(denied) {
		restarted.Stop()
		t.Fatal("durable generation not restored", state, err)
	}
	restarted.Stop()
	state, err = egresscontrol.Call(ctx, dir, nil)
	if err == nil {
		t.Fatal("stopped control still available")
	}
	if err = os.WriteFile(filepath.Join(dir, "policy.json"), []byte(`{broken`), 0600); err != nil {
		t.Fatal(err)
	}
	corrupt, err := NewProxy(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = corrupt.Start(); err == nil {
		corrupt.Stop()
		t.Fatal("corrupt policy fell back to launch arguments")
	}
}
func TestLivePolicyPersistenceFailureKeepsDenyState(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	p, err := NewProxy(Config{Role: RoleEgress, ListenAddr: "127.0.0.1:0", PolicyControlDir: dir, ForwardPolicy: networkpolicy.Rules{DefaultDeny: true, BlockPrivate: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	if err = os.Remove(filepath.Join(dir, "policy.json")); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(dir, "policy.json"), 0700); err != nil {
		t.Fatal(err)
	}
	_, err = egresscontrol.Call(context.Background(), dir, &egresscontrol.Update{ExpectedGeneration: 1, Rules: networkpolicy.Rules{BlockPrivate: true, DefaultDeny: true, AllowDomains: []string{"=example.com"}}})
	if err == nil {
		t.Fatal("failed persistence reported complete")
	}
	p.egressMu.Lock()
	ready := p.egressEpoch.state.Ready
	stopped := p.egressEpoch.ctx.Err() != nil
	p.egressMu.Unlock()
	if ready || !stopped {
		t.Fatal("storage failure reopened egress")
	}
}

func TestPendingPolicyBarrierCannotBeBypassedByRetry(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	p, err := NewProxy(Config{Role: RoleEgress, PolicyControlDir: dir, ForwardPolicy: networkpolicy.Rules{DefaultDeny: true, BlockPrivate: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err = p.startEgressControl(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	p.egressEpoch.active.Add(1)
	old := p.egressEpoch
	released := false
	defer func() {
		if !released {
			old.active.Done()
		}
	}()
	apply := func(generation uint64) (int, egresscontrol.State) {
		raw, _ := json.Marshal(egresscontrol.Update{ExpectedGeneration: generation, Rules: networkpolicy.Rules{DefaultDeny: true, BlockPrivate: true}})
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		req := httptest.NewRequest("POST", "http://local/policy", strings.NewReader(string(raw))).WithContext(ctx)
		recorder := httptest.NewRecorder()
		p.egressControlHTTP(recorder, req)
		var state egresscontrol.State
		if err := json.Unmarshal(recorder.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		return recorder.Code, state
	}
	code, state := apply(1)
	if code != 503 || state.Ready || state.Generation != 2 {
		t.Fatal("incomplete barrier reported complete", code, state)
	}
	code, state = apply(2)
	if code != 503 || state.Ready || state.Generation != 3 {
		t.Fatal("retry bypassed original active request", code, state)
	}
	old.active.Done()
	released = true
	code, state = apply(3)
	if code != 200 || !state.Ready || state.Generation != 4 {
		t.Fatal("released barrier did not recover", code, state)
	}
}
