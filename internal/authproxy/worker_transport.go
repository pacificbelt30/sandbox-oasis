package authproxy

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"github.com/pacificbelt30/codex-dock/internal/strictjson"
	"net/http"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/workertransport"
)

// WorkerTransportService deliberately requires separate protected admin access.
// The returned key is a disposable run credential, never an upstream credential.
type WorkerTransportService interface {
	WorkerTransportEnabled() bool
	WorkerTransport(context.Context, string, int) (workertransport.Config, error)
}

func (p *Proxy) WorkerTransportEnabled() bool { return p.transportCA != nil }
func (p *Proxy) WorkerTransport(_ context.Context, token string, ttl int) (workertransport.Config, error) {
	if p.transportCA == nil {
		return workertransport.Config{}, fmt.Errorf("worker TLS unavailable")
	}
	config, err := p.transportCA.Issue(token, ttl)
	if err != nil {
		return config, err
	}
	block, _ := pem.Decode(config.Certificate)
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return workertransport.Config{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	rec := p.tokensByValue[token]
	if rec == nil || rec.transportKeyHash != "" || !time.Now().Before(rec.ExpiresAt) {
		return workertransport.Config{}, fmt.Errorf("worker token is inactive or already bound")
	}
	rec.transportKeyHash = workertransport.Binding(string(leaf.RawSubjectPublicKeyInfo))
	return config, nil
}
func (p *Proxy) transportBound(r *http.Request, token string) bool {
	if p.transportCA == nil {
		return true
	}
	if !workertransport.Bound(r.TLS, token) {
		return false
	}
	leaf := r.TLS.VerifiedChains[0][0]
	now := time.Now()
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	rec := p.tokensByValue[token]
	return rec != nil && rec.transportKeyHash != "" && rec.transportKeyHash == workertransport.Binding(string(r.TLS.VerifiedChains[0][0].RawSubjectPublicKeyInfo))
}
func (p *Proxy) handleAdminTransport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !p.requireAdmin(w, r) {
		return
	}
	var in struct {
		Token string `json:"token"`
		TTL   int    `json:"ttl"`
	}
	if err := strictjson.Decode(r.Body, 4096, &in); err != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	c, err := p.WorkerTransport(r.Context(), in.Token, in.TTL)
	if err != nil {
		http.Error(w, "invalid worker transport lease", 400)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(c)
}
func (r *RemoteProxy) WorkerTransportEnabled() bool { return r.workerMTLS }
func (r *RemoteProxy) WorkerTransport(ctx context.Context, token string, ttl int) (workertransport.Config, error) {
	if !r.workerMTLS {
		return workertransport.Config{}, fmt.Errorf("worker TLS unavailable")
	}
	data, _ := json.Marshal(map[string]any{"token": token, "ttl": ttl})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.adminURL+"/admin/transport", bytes.NewReader(data))
	if err != nil {
		return workertransport.Config{}, err
	}
	req.Header.Set(r.adminSecretHeader, r.adminSecret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return workertransport.Config{}, fmt.Errorf("worker TLS issuance unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return workertransport.Config{}, fmt.Errorf("worker TLS issuance rejected (HTTP %d)", resp.StatusCode)
	}
	var c workertransport.Config
	err = json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 64<<10)).Decode(&c)
	return c, err
}

func (p *Proxy) validateLeaseTTL(ttl int) error {
	maximum := 365 * 24 * 3600
	if p.WorkerTransportEnabled() {
		maximum = workertransport.MaxTTL
	}
	if ttl < 1 || ttl > maximum {
		return fmt.Errorf("invalid worker token TTL (maximum %d seconds)", maximum)
	}
	return nil
}
