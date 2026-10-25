package authproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/pacificbelt30/codex-dock/internal/policyevents"
	"github.com/pacificbelt30/codex-dock/internal/usage"
	"github.com/pacificbelt30/codex-dock/pkg/cxd/identity"
)

type usagePolicyKey struct{}
type tokenUsage struct {
	key    string
	policy *identity.UsagePolicy
}

func (p *Proxy) SupportsTokenUsagePolicy() bool       { return p.usageLedger != nil }
func (r *RemoteProxy) SupportsTokenUsagePolicy() bool { return r.tokenUsagePolicy }

// PrepareUsage reserves immutable budget metadata, never an active token. Kata
// can prepare before boot and still activate its placeholder only after checks.
func (p *Proxy) PrepareUsage(name string, id identity.Identity, policy *identity.UsagePolicy) error {
	if name == "" || len(name) > 255 || strings.ContainsAny(name, "\r\n\x00") {
		return fmt.Errorf("invalid usage worker name")
	}
	if policy == nil {
		return fmt.Errorf("usage policy required")
	}
	if err := p.checkTokenIdentity(id); err != nil {
		return err
	}
	rec := &tokenRecord{ContainerName: name, Identity: id}
	if err := p.bindTokenUsage(rec, policy); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.caseRevoked || p.revokedRuns[id] {
		return fmt.Errorf("credential case or run was revoked")
	}
	if p.usageBindings == nil {
		p.usageBindings = make(map[string]string)
	}
	p.usageBindings[name] = rec.usageKey
	return nil
}

func (r *RemoteProxy) PrepareUsage(name string, id identity.Identity, policy *identity.UsagePolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	if !r.tokenUsagePolicy || policy == nil {
		return fmt.Errorf("auth proxy does not support durable execution usage limits")
	}
	data, _ := json.Marshal(map[string]any{"container": name, "identity": id, "usage_policy": policy})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, r.adminURL+"/admin/usage/prepare", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set(r.adminSecretHeader, r.adminSecret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("preparing execution usage policy failed (HTTP %d)", resp.StatusCode)
	}
	return nil
}

func (p *Proxy) handleAdminPrepareUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !p.requireAdmin(w, r) {
		return
	}
	var req struct {
		Container   string                `json:"container"`
		Identity    identity.Identity     `json:"identity"`
		UsagePolicy *identity.UsagePolicy `json:"usage_policy"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		http.Error(w, "invalid usage preparation", http.StatusBadRequest)
		return
	}
	if err := p.PrepareUsage(req.Container, req.Identity, req.UsagePolicy); err != nil {
		http.Error(w, "usage preparation failed", http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r *RemoteProxy) UsageContext(ctx context.Context, container string) (usage.Snapshot, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.adminURL+"/admin/usage?container="+url.QueryEscape(container), nil)
	if err != nil {
		return usage.Snapshot{}, err
	}
	req.Header.Set(r.adminSecretHeader, r.adminSecret)
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return usage.Snapshot{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return usage.Snapshot{}, fmt.Errorf("execution usage unavailable (HTTP %d)", resp.StatusCode)
	}
	var snapshot usage.Snapshot
	err = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&snapshot)
	return snapshot, err
}

func (p *Proxy) bindTokenUsage(rec *tokenRecord, policy *identity.UsagePolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	if p.usageLedger == nil {
		if policy != nil {
			return fmt.Errorf("usage policy requires an owner-private durable usage-state directory")
		}
		return nil
	}
	rec.usageKey = usage.Key(rec.ContainerName, rec.Identity)
	bound, err := p.usageLedger.Bind(rec.usageKey, policy)
	if err != nil {
		return err
	}
	rec.UsagePolicy = bound
	return nil
}

func (p *Proxy) meterUsage(w http.ResponseWriter, r *http.Request) (http.ResponseWriter, *http.Request, func(), bool) {
	budget, _ := r.Context().Value(usagePolicyKey{}).(*tokenUsage)
	if budget == nil {
		return w, r, func() {}, true
	}
	deny := func(code string, status int) (http.ResponseWriter, *http.Request, func(), bool) {
		w.Header().Set("X-Cxd-Policy-Reason", code)
		http.Error(w, code, status)
		p.events.Add(policyevents.Event{Code: code})
		return w, r, func() {}, false
	}
	if isWebSocketRequest(r) {
		return deny("usage_policy_websocket_unsupported", http.StatusForbidden)
	}
	if budget.policy.MaxTokens > 0 && !tokenUsageRoute(r) && !usageMetadataRoute(r) {
		return deny("usage_policy_route_unsupported", http.StatusForbidden)
	}
	if p.usageLedger == nil {
		return deny("usage_state_unavailable", http.StatusServiceUnavailable)
	}
	request, err := p.usageLedger.Begin(budget.key)
	if errors.Is(err, usage.ErrLimit) {
		return deny("usage_limit", http.StatusTooManyRequests)
	}
	if err != nil {
		return deny("usage_state_unavailable", http.StatusServiceUnavailable)
	}
	writer := &usageWriter{ResponseWriter: w, request: request}
	if tokenUsageRoute(r) {
		writer.observer = &usageObserver{}
	}
	if r.Body != nil {
		r.Body = &usageBody{ReadCloser: r.Body, request: request}
	}
	finish := func() {
		tokens, known := int64(0), usageMetadataRoute(r)
		if writer.observer != nil {
			tokens, known = writer.observer.Result(w.Header().Get("Content-Type"))
		}
		if err := request.Finish(tokens, known); err != nil {
			p.events.Add(policyevents.Event{Code: "usage_state_unavailable"})
		}
	}
	return writer, r, finish, true
}

func tokenUsageRoute(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	switch r.URL.Path {
	case "/v1/responses", "/v1/responses/compact", "/v1/chat/completions", "/v1/completions", "/v1/embeddings", "/v1/moderations", "/chatgpt/codex/responses", "/chatgpt/codex/responses/compact", "/anthropic/v1/messages":
		return true
	}
	return false
}

func usageMetadataRoute(r *http.Request) bool {
	if r.Method == http.MethodPost && r.URL.Path == "/anthropic/v1/messages/count_tokens" {
		return true
	}
	if r.Method != http.MethodGet {
		return false
	}
	switch r.URL.Path {
	case "/v1/models", "/chatgpt/codex/models", "/anthropic/v1/models":
		return true
	}
	return false
}

type usageBody struct {
	io.ReadCloser
	request *usage.Request
}

func (b *usageBody) Read(data []byte) (int, error) {
	n, err := b.ReadCloser.Read(data)
	allowed, limitErr := b.request.Consume(n)
	if limitErr != nil {
		return allowed, limitErr
	}
	return allowed, err
}

type usageWriter struct {
	http.ResponseWriter
	request  *usage.Request
	observer *usageObserver
}

func (w *usageWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *usageWriter) Flush()                      { _ = http.NewResponseController(w.ResponseWriter).Flush() }
func (w *usageWriter) Write(data []byte) (int, error) {
	allowed, err := w.request.Consume(len(data))
	if allowed == 0 && err != nil {
		return 0, err
	}
	n, writeErr := w.ResponseWriter.Write(data[:allowed])
	if w.observer != nil {
		w.observer.Observe(data[:n], w.Header().Get("Content-Type"))
	}
	if writeErr != nil {
		return n, writeErr
	}
	return n, err
}

func (p *Proxy) Usage(container string) (usage.Snapshot, error) {
	p.mu.RLock()
	key := p.usageBindings[container]
	p.mu.RUnlock()
	if p.usageLedger == nil || key == "" {
		return usage.Snapshot{}, fmt.Errorf("no metered execution is bound to this worker")
	}
	return p.usageLedger.Inspect(key)
}

func (p *Proxy) UsageContext(ctx context.Context, container string) (usage.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return usage.Snapshot{}, err
	}
	return p.Usage(container)
}

func (p *Proxy) handleAdminUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !p.requireAdmin(w, r) {
		return
	}
	snapshot, err := p.Usage(r.URL.Query().Get("container"))
	if err != nil {
		http.Error(w, "usage unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(snapshot)
}
