package authproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/pacificbelt30/codex-dock/internal/privatefile"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/pacificbelt30/codex-dock/pkg/cxd/identity"
)

// CredentialRoute is trusted host configuration. The worker selects a route
// name and a permitted path, never an upstream or an arbitrary secret header.
// Secret is evaluated after live-token validation and is never exposed in APIs.
type CredentialRoute struct {
	Name     string
	Upstream string
	Paths    []string
	Methods  []string
	Header   string
	Prefix   string
	Provider string // empty/any, openai, or anthropic
	Secret   func(context.Context) (string, error)
}

var routeName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

func validateRoutes(routes []CredentialRoute) error {
	names := map[string]bool{}
	for _, r := range routes {
		u, err := url.Parse(r.Upstream)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
			return fmt.Errorf("credential route requires a fixed HTTPS origin")
		}
		if !routeName.MatchString(r.Name) || names[r.Name] || r.Secret == nil || len(r.Paths) == 0 || len(r.Methods) == 0 {
			return fmt.Errorf("invalid or duplicate credential route")
		}
		names[r.Name] = true
		if r.Provider != "" && r.Provider != "any" && r.Provider != "openai" && r.Provider != "anthropic" {
			return fmt.Errorf("invalid credential route provider")
		}
		if r.Header == "" || strings.ContainsAny(r.Header, ": \t\r\n") || strings.ContainsAny(r.Prefix, "\r\n") {
			return fmt.Errorf("invalid credential header")
		}
		switch strings.ToLower(r.Header) {
		case "host", "connection", "proxy-authorization", "proxy-authenticate", "content-length", "transfer-encoding", "upgrade", "cookie", "set-cookie", "x-codex-token":
			return fmt.Errorf("unsafe credential header")
		}
		for _, p := range r.Paths {
			if !strings.HasPrefix(p, "/") || path.Clean(p) != p || strings.ContainsAny(p, "%?#\\") {
				return fmt.Errorf("invalid credential path scope")
			}
		}
		for _, m := range r.Methods {
			switch m {
			case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
			default:
				return fmt.Errorf("invalid credential route method")
			}
		}
	}
	return nil
}
func (p *Proxy) brokerProviderAllowed(provider, path string) bool {
	if !strings.HasPrefix(path, "/broker/") {
		return providerRouteAllowed(provider, path)
	}
	name, _, _ := strings.Cut(strings.TrimPrefix(path, "/broker/"), "/")
	for _, r := range p.cfg.CredentialRoutes {
		if r.Name == name {
			return provider == "" || provider == "any" || r.Provider == "" || r.Provider == "any" || r.Provider == provider
		}
	}
	return false
}
func (p *Proxy) handleBroker(w http.ResponseWriter, r *http.Request) {
	name, relative, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/broker/"), "/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	var route *CredentialRoute
	for i := range p.cfg.CredentialRoutes {
		if p.cfg.CredentialRoutes[i].Name == name {
			route = &p.cfg.CredentialRoutes[i]
			break
		}
	}
	if route == nil {
		http.NotFound(w, r)
		return
	}
	authenticated, done, valid := p.workerRequest(r, false)
	if !valid {
		http.Error(w, "worker token invalid or expired", http.StatusUnauthorized)
		return
	}
	defer done()
	r = authenticated
	// Arbitrary credential routes have no model schema. A restricted token
	// cannot use one to bypass authorization on the fixed AI routes.
	if policy, _ := r.Context().Value(modelPolicyKey{}).(*identity.ModelPolicy); policy != nil {
		w.Header().Set("X-Cxd-Policy-Reason", "model_policy_route_denied")
		http.Error(w, "model_policy_route_denied", http.StatusForbidden)
		return
	}
	requested := "/" + relative
	// Disallow alternate encodings and path normalization surprises before injecting.
	if r.URL.RawPath != "" || strings.ContainsAny(requested, "%\\") || path.Clean(requested) != requested {
		http.Error(w, "path not permitted", http.StatusForbidden)
		return
	}
	permitted := false
	for _, prefix := range route.Paths {
		if requested == prefix || strings.HasPrefix(requested, strings.TrimSuffix(prefix, "/")+"/") {
			permitted = true
		}
	}
	methodOK := false
	for _, m := range route.Methods {
		if m == r.Method {
			methodOK = true
		}
	}
	if !permitted || !methodOK || isWebSocketRequest(r) {
		http.Error(w, "route scope denied", http.StatusForbidden)
		return
	}
	w, r, finishUsage, allowed := p.meterUsage(w, r)
	if !allowed {
		return
	}
	defer finishUsage()
	secret, err := route.Secret(r.Context())
	if err != nil || secret == "" || strings.ContainsAny(secret, "\r\n") {
		http.Error(w, "credential unavailable", http.StatusServiceUnavailable)
		return
	}
	target := strings.TrimRight(route.Upstream, "/") + requested
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		http.Error(w, "invalid upstream request", http.StatusBadRequest)
		return
	}
	req.ContentLength = r.ContentLength
	if !enableStreamingResponse(w) {
		return
	}
	for _, key := range []string{"Accept", "Content-Type", "User-Agent", "Idempotency-Key"} {
		if v := r.Header.Get(key); v != "" {
			req.Header.Set(key, v)
		}
	}
	req.Header.Set(route.Header, route.Prefix+secret)
	client := *p.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "credential route upstream unavailable", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	for _, key := range []string{"Content-Type", "Retry-After", "X-Request-Id"} {
		if v := resp.Header.Get(key); v != "" {
			w.Header().Set(key, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	if err := copyAndFlush(w, resp.Body); err != nil {
		panic(http.ErrAbortHandler)
	}
}

func cloneRoutes(routes []CredentialRoute) []CredentialRoute {
	out := append([]CredentialRoute(nil), routes...)
	for i := range out {
		out[i].Paths = append([]string(nil), out[i].Paths...)
		out[i].Methods = append([]string(nil), out[i].Methods...)
	}
	return out
}

// LoadCredentialRoutes reads an owner-private host/proxy file. Its secret
// values remain inside the auth proxy, never in labels, argv or worker mounts.
func LoadCredentialRoutes(path string) ([]CredentialRoute, error) {
	data, err := privatefile.Read(path)
	if err != nil {
		return nil, err
	}
	var records []struct {
		Name, Upstream, Header, Prefix, Provider, Secret string
		Paths, Methods                                   []string
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&records); err != nil {
		return nil, fmt.Errorf("invalid credential route configuration")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, fmt.Errorf("invalid credential route configuration")
	}
	var routes []CredentialRoute
	for _, r := range records {
		if r.Secret == "" || strings.ContainsAny(r.Secret, "\r\n") {
			return nil, fmt.Errorf("credential route secret is missing or invalid")
		}
		secret := r.Secret
		routes = append(routes, CredentialRoute{Name: r.Name, Upstream: r.Upstream, Header: r.Header, Prefix: r.Prefix, Provider: r.Provider, Paths: r.Paths, Methods: r.Methods, Secret: func(context.Context) (string, error) { return secret, nil }})
	}
	if err = validateRoutes(routes); err != nil {
		return nil, err
	}
	return routes, nil
}

// ValidateCredentialRoutes validates trusted scopes without connecting or resolving secrets.
func ValidateCredentialRoutes(routes []CredentialRoute) error { return validateRoutes(routes) }
