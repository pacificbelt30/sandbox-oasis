//go:build linux

package engine

import (
	"bytes"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/pacificbelt30/codex-dock/internal/auditlog"
)

type auditResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (w *auditResponse) Header() http.Header { return w.header }
func (w *auditResponse) WriteHeader(s int) {
	if w.status == 0 {
		w.status = s
	}
}
func (w *auditResponse) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return w.body.Write(b)
}
func nodeAuditAction(r *http.Request) (string, string) {
	path := r.URL.Path
	if strings.HasPrefix(path, "/v1.") {
		if i := strings.Index(path[1:], "/"); i >= 0 {
			path = path[i+1:]
		}
	}
	if r.Method == http.MethodDelete && (strings.HasPrefix(path, "/containers/") || strings.HasPrefix(path, "/checkpoints/")) {
		return "delete", path
	}
	if r.Method != http.MethodPost {
		return "", ""
	}
	if path == "/containers/create" {
		return "create", r.URL.Query().Get("name")
	}
	if path == "/checkpoints" {
		return "checkpoint", ""
	}
	if strings.HasPrefix(path, "/containers/") {
		parts := strings.Split(strings.TrimPrefix(path, "/containers/"), "/")
		if len(parts) == 2 {
			switch parts[1] {
			case "start":
				return "start", parts[0]
			case "stop", "kill", "quarantine":
				return "stop", parts[0]
			}
		}
	}
	return "", ""
}
func (b *Backend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	action, subject := nodeAuditAction(r)
	// Unit fixtures without a node state directory do not represent a serving node.
	if action == "" || b.opts.State == "" {
		b.serveHTTP(w, r)
		return
	}
	j := auditlog.Journal{Directory: filepath.Join(b.opts.State, "audit")}
	finish, err := j.Begin(action, subject)
	if err != nil {
		if action != "stop" && action != "delete" {
			http.Error(w, "audit unavailable; operation refused", http.StatusServiceUnavailable)
			return
		}
		// Never let unavailable evidence prevent revocation or resource shutdown.
		log.Print("audit unavailable during restrictive node operation")
		b.serveHTTP(w, r)
		return
	}
	response := &auditResponse{header: make(http.Header)}
	defer func() {
		if v := recover(); v != nil {
			_ = finish(http.ErrAbortHandler)
			panic(v)
		}
	}()
	b.serveHTTP(response, r)
	if response.status == 0 {
		response.status = 200
	}
	var outcome error
	if response.status >= 400 {
		outcome = http.ErrAbortHandler
	}
	if err = finish(outcome); err != nil {
		log.Print("audit result unavailable; reconcile operation state")
		http.Error(w, "audit result unavailable; operation outcome unknown; inspect before retry", http.StatusServiceUnavailable)
		return
	}
	for k, values := range response.header {
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(response.status)
	_, _ = w.Write(response.body.Bytes())
}
