package authproxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"github.com/pacificbelt30/codex-dock/internal/egresscontrol"
	"github.com/pacificbelt30/codex-dock/internal/networkpolicy"
	"github.com/pacificbelt30/codex-dock/internal/strictjson"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type egressEpoch struct {
	state     egresscontrol.State
	policy    networkpolicy.Policy
	transport *http.Transport
	ctx       context.Context
	cancel    context.CancelFunc
	active    sync.WaitGroup
}

func (p *Proxy) newEgressEpoch(state egresscontrol.State, policy networkpolicy.Policy) *egressEpoch {
	ctx, cancel := context.WithCancel(context.Background())
	transport := newProxyTransport((networkpolicy.Dialer{Policy: policy, Connect: p.connectVerified}).DialContext)
	transport.TLSClientConfig = &tls.Config{RootCAs: p.rootCAs, MinVersion: tls.VersionTLS12}
	context.AfterFunc(ctx, transport.CloseIdleConnections)
	return &egressEpoch{state: state, policy: policy, ctx: ctx, cancel: cancel, transport: transport}
}

// saveEgressState persists intent before cutting connections. A crash resumes
// only the new policy; an unreadable state is fatal, never a fallback to args.
func (p *Proxy) saveEgressState(s egresscontrol.State) error {
	dir := p.cfg.PolicyControlDir
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, "state-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, filepath.Join(dir, "policy.json")); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}
func (p *Proxy) startEgressControl() error {
	dir := p.cfg.PolicyControlDir
	if dir == "" {
		return nil
	}
	if !p.cfg.isEgress() || !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || p.cfg.EgressPolicy != nil {
		return fmt.Errorf("live policy requires a private egress-only directory and declarative rules")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("egress control directory must be private (0700)")
	}
	rules := p.cfg.ForwardPolicy
	if len(p.cfg.ForwardAllowDomains) > 0 {
		rules.AllowDomains = p.cfg.ForwardAllowDomains
	}
	rules.BlockPrivate = true
	state := egresscontrol.State{Generation: 1, Rules: rules, PolicyID: egresscontrol.ID(rules), Ready: true}
	if p.cfg.AuditDirectory == "" {
		p.cfg.AuditDirectory = filepath.Join(dir, "audit")
	}
	file := filepath.Join(dir, "policy.json")
	if f, err := os.Open(file); err == nil {
		info, statErr := f.Stat()
		if statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			_ = f.Close()
			return fmt.Errorf("unsafe durable egress policy")
		}
		data, readErr := io.ReadAll(io.LimitReader(f, 64<<10+1))
		_ = f.Close()
		if readErr != nil || len(data) > 64<<10 || strictjson.Decode(bytes.NewReader(data), 64<<10, &state) != nil || state.Generation == 0 || state.PolicyID != egresscontrol.ID(state.Rules) || !state.Rules.BlockPrivate {
			return fmt.Errorf("invalid durable egress policy")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	compiled, err := networkpolicy.Compile(state.Rules)
	if err != nil {
		return err
	}
	state.Ready = true // No pre-restart connection survives a process restart.
	if err = p.saveEgressState(state); err != nil {
		return err
	}
	p.egressEpoch = p.newEgressEpoch(state, compiled)
	socket := filepath.Join(dir, egresscontrol.Socket)
	if info, err := os.Lstat(socket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return fmt.Errorf("invalid control socket path")
		}
		if err = os.Remove(socket); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", socket)
	if err != nil {
		return err
	}
	if err = os.Chmod(socket, 0600); err != nil {
		_ = listener.Close()
		return err
	}
	p.egressAdmin = &http.Server{Handler: http.HandlerFunc(p.egressControlHTTP), ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 4096}
	go func() { _ = p.egressAdmin.Serve(listener) }()
	return nil
}
func (p *Proxy) egressControlHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path != "/policy" {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodGet {
		p.egressMu.Lock()
		state := p.egressEpoch.state
		p.egressMu.Unlock()
		_ = json.NewEncoder(w).Encode(state)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var update egresscontrol.Update
	if strictjson.Decode(r.Body, 64<<10, &update) != nil || !update.Rules.BlockPrivate {
		w.WriteHeader(400)
		return
	}
	compiled, err := networkpolicy.Compile(update.Rules)
	if err != nil {
		w.WriteHeader(400)
		return
	}
	p.egressMu.Lock()
	defer p.egressMu.Unlock()
	old := p.egressEpoch
	if update.ExpectedGeneration != old.state.Generation || old.state.Generation >= 1<<53-1 {
		w.WriteHeader(409)
		_ = json.NewEncoder(w).Encode(old.state)
		return
	}
	finish, auditErr := p.auditBegin("permission_change", old.state.PolicyID)
	if auditErr != nil {
		w.WriteHeader(503)
		_ = json.NewEncoder(w).Encode(old.state)
		return
	}
	auditFinished := false
	defer func() {
		if !auditFinished {
			_ = finish(fmt.Errorf("policy update unconfirmed"))
		}
	}()
	state := egresscontrol.State{Generation: old.state.Generation + 1, PolicyID: egresscontrol.ID(update.Rules), Rules: update.Rules, Ready: false}
	if err = p.saveEgressState(state); err != nil {
		// A rename may already have succeeded. Deny until an operator reconciles.
		old.cancel()
		old.state.Ready = false
		w.WriteHeader(503)
		_ = json.NewEncoder(w).Encode(old.state)
		return
	}
	old.cancel()
	next := p.newEgressEpoch(state, compiled)
	next.cancel() // Block until the barrier and durable completion succeed.
	p.egressEpoch = next
	done := make(chan struct{})
	prior := p.egressPending
	go func() {
		old.active.Wait()
		if prior != nil {
			<-prior
		}
		close(done)
	}()
	p.egressPending = done
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
		state.Ready = true
		if err = p.saveEgressState(state); err == nil {
			auditErr = finish(nil)
			auditFinished = true
			if auditErr != nil {
				state.Ready = false
				_ = p.saveEgressState(state)
				w.WriteHeader(http.StatusServiceUnavailable)
				_ = json.NewEncoder(w).Encode(state)
				return
			}
			p.egressEpoch = p.newEgressEpoch(state, compiled)
			_ = json.NewEncoder(w).Encode(state)
			return
		}
	case <-timer.C:
	case <-r.Context().Done():
	}
	w.WriteHeader(503)
	_ = json.NewEncoder(w).Encode(p.egressEpoch.state)
}

// beginEgress takes a lease before dial. Policy changes cancel every leased
// request, including DNS resolution, redirects, streaming responses and CONNECT.
func (p *Proxy) beginEgress(w http.ResponseWriter, r *http.Request) (*http.Request, func(), bool) {
	p.egressMu.Lock()
	epoch := p.egressEpoch
	if epoch == nil {
		p.egressMu.Unlock()
		return r, func() {}, true
	}
	if !epoch.state.Ready || epoch.ctx.Err() != nil {
		p.egressMu.Unlock()
		http.Error(w, "policy update pending", http.StatusServiceUnavailable)
		return nil, nil, false
	}
	epoch.active.Add(1)
	p.egressMu.Unlock()
	ctx, cancel := context.WithCancel(r.Context())
	callbackDone := make(chan struct{})
	stop := context.AfterFunc(epoch.ctx, func() {
		defer close(callbackDone)
		cancel()
		if conn, ok := r.Context().Value(workerConnectionKey{}).(net.Conn); ok {
			_ = conn.Close()
		}
	})
	ctx = context.WithValue(ctx, egressPolicyKey{}, epoch)
	return r.WithContext(ctx), func() {
		if !stop() {
			<-callbackDone
		}
		cancel()
		epoch.active.Done()
	}, true
}

type egressPolicyKey struct{}
