package credentialstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/pacificbelt30/codex-dock/internal/strictjson"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type loadRequest struct {
	CaseID string `json:"case_id"`
}
type saveRequest struct {
	CaseID      string      `json:"case_id"`
	Version     string      `json:"version"`
	Provider    string      `json:"provider"`
	Credentials Credentials `json:"credentials"`
}
type loadReply struct {
	Credentials Credentials `json:"credentials"`
}
type saveReply struct {
	Version string `json:"version"`
}

// Serve exposes one case through a private Unix socket. Use a distinct socket
// per case so a proxy cannot request another case's credentials. Only the
// matching auth proxy may bind-mount this socket; workers must never mount it.
func Serve(ctx context.Context, path, caseID string, store Store) error {
	if store == nil || caseID == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("credential store requires a clean absolute socket path, case ID and store")
	}
	parent := filepath.Dir(path)
	info, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm()&0077 != 0 || stat.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("credential store socket parent must be a private directory owned by this process")
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("credential store socket already exists")
	} else if !os.IsNotExist(err) {
		return err
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "unix", path)
	if err != nil {
		return err
	}
	socketInfo, err := os.Lstat(path)
	if err != nil {
		_ = ln.Close()
		return err
	}
	defer func() {
		if current, err := os.Lstat(path); err == nil && os.SameFile(socketInfo, current) {
			_ = os.Remove(path)
		}
		_ = ln.Close()
	}()
	if err := os.Chmod(path, 0600); err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /load", func(w http.ResponseWriter, r *http.Request) {
		var in loadRequest
		if !decodeRequest(w, r, &in) {
			return
		}
		if in.CaseID != caseID {
			http.Error(w, "credential case not found", http.StatusNotFound)
			return
		}
		creds, err := store.Load(r.Context(), in.CaseID)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, loadReply{Credentials: creds})
	})
	mux.HandleFunc("POST /save", func(w http.ResponseWriter, r *http.Request) {
		var in saveRequest
		if !decodeRequest(w, r, &in) {
			return
		}
		if in.CaseID != caseID {
			http.Error(w, "credential case not found", http.StatusNotFound)
			return
		}
		if in.Version == "" || (in.Provider != "openai" && in.Provider != "anthropic") {
			http.Error(w, "invalid refresh request", http.StatusBadRequest)
			return
		}
		version, err := store.SaveRefresh(r.Context(), in.CaseID, in.Version, in.Provider, in.Credentials)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		if version == "" {
			http.Error(w, "store returned empty version", http.StatusBadGateway)
			return
		}
		writeJSON(w, saveReply{Version: version})
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	stop := context.AfterFunc(ctx, func() { _ = server.Close() })
	defer stop()
	err = server.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func decodeRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	if err := strictjson.Decode(r.Body, 1<<20, target); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return false
	}
	return true
}

func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		http.Error(w, "credential connection not found", http.StatusNotFound)
	case errors.Is(err, ErrConflict):
		http.Error(w, "credential connection changed", http.StatusConflict)
	default:
		http.Error(w, "credential store unavailable", http.StatusBadGateway)
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

// UnixClient implements Store for an auth proxy process that has the socket
// bind-mounted. It carries real credentials only over local Unix IPC.
type UnixClient struct {
	http *http.Client
}

func NewUnixClient(path string) (*UnixClient, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("invalid credential store socket path")
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", path)
	}}
	return &UnixClient{http: &http.Client{Transport: transport, Timeout: 15 * time.Second}}, nil
}

func (c *UnixClient) Load(ctx context.Context, caseID string) (Credentials, error) {
	var out loadReply
	err := c.call(ctx, "/load", loadRequest{CaseID: caseID}, &out)
	return out.Credentials, err
}

func (c *UnixClient) SaveRefresh(ctx context.Context, caseID, version, provider string, credentials Credentials) (string, error) {
	var out saveReply
	err := c.call(ctx, "/save", saveRequest{CaseID: caseID, Version: version, Provider: provider, Credentials: credentials}, &out)
	return out.Version, err
}

func (c *UnixClient) call(ctx context.Context, path string, in, out any) error {
	data, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://credential-store"+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case http.StatusNotFound:
			return ErrNotFound
		case http.StatusConflict:
			return ErrConflict
		default:
			return fmt.Errorf("credential store returned HTTP %d", resp.StatusCode)
		}
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}
