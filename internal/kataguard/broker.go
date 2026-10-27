package kataguard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/pacificbelt30/codex-dock/internal/buildinfo"
	"github.com/pacificbelt30/codex-dock/internal/engine"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type NodeInfo struct {
	ProtocolVersion                          int
	BuildVersion                             string
	Capabilities                             []string
	Profile, Endpoint, Stamp, Launcher, Root string
	SharedFSCache                            string
	Protected                                []string
}
type nodeRequest struct {
	Action, Runtime, Allocation, Worker, Endpoint, ResourceProfile string
	RequestKey                                                     string `json:"RequestKey,omitempty"`
	Fingerprint                                                    string `json:"Fingerprint,omitempty"`
	Lease                                                          string `json:"Lease,omitempty"`
	WorkerID                                                       string `json:"WorkerID,omitempty"`
	Force                                                          bool
}
type nodeReply struct {
	Error, Code, Parent string
	Info                NodeInfo
	Inventory           Inventory
	Launch              LaunchRecord
	Profiles            []ResourceProfile
}

// Serve hosts the containerd engine and fixed Kata verification profile.
// The socket must be owned by root and accessible only to trusted operators.
// Engine access is root-equivalent: operators choose images, commands and host
// bind mounts. Guests never receive this socket. Orphan recovery remains scoped
// to attested envelopes; there is no TCP management endpoint.
func Serve(ctx context.Context, cfg Config, socket string, gid int) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("kata node service must run as root")
	}
	if !cleanPath(socket) {
		return fmt.Errorf("invalid node socket path")
	}
	if err := trustedPath(filepath.Dir(socket), true); err != nil {
		return err
	}
	if info, err := os.Lstat(socket); err == nil {
		if info.Mode()&os.ModeSocket == 0 || !rootOwner(info) {
			return fmt.Errorf("untrusted existing node socket")
		}
		conn, dialErr := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", socket)
		if conn != nil {
			_ = conn.Close()
		}
		if !errors.Is(dialErr, syscall.ECONNREFUSED) {
			return fmt.Errorf("node socket is active or cannot be safely replaced")
		}
		current, statErr := os.Lstat(socket)
		if statErr != nil || !os.SameFile(info, current) {
			return fmt.Errorf("node socket changed during recovery")
		}
		if err := os.Remove(socket); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	g, err := New(cfg)
	if err != nil {
		return err
	}
	if err = g.Validate(ctx, g.manifest.Runtime); err != nil {
		return err
	}
	_, daemon, err := readConfig(g.manifest.Assets["config"].Path, g.manifest.Assets["daemon"].Path, g.manifest.Runtime)
	if err != nil {
		return err
	}
	tools := map[string]string{}
	for _, name := range []string{"ip", "nft", "sysctl", "runc", "runc-shim", "mkfs"} {
		tools[name] = g.manifest.Assets[name].Path
	}
	if !g.agentState {
		delete(tools, "mkfs")
	}
	backend, err := engine.Open(engine.Options{Address: daemon.GRPC.Address, State: filepath.Join(cfg.StateDir, "engine"), Runtime: g.manifest.Assets["runtime"].Path, KataConfig: g.manifest.Assets["config"].Path, Retire: g.Retire, AbortCreate: g.AbortCreate, ConfirmStopped: g.ConfirmStopped, Tools: tools, Logger: g.manifest.Assets["logger"].Path, DNS: cfg.DNS, TemporaryStorage: g.temporaryStorage})
	if err != nil {
		return err
	}
	defer func() { _ = backend.Close() }()
	listener, err := (&net.ListenConfig{}).Listen(ctx, "unix", socket)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close(); _ = os.Remove(socket) }()
	if err = os.Chown(socket, 0, gid); err != nil {
		return err
	}
	if err = os.Chmod(socket, 0660); err != nil {
		return err
	}
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	var launchMu sync.Mutex
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/node" {
			backend.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/node" {
			http.NotFound(w, r)
			return
		}
		var req nodeRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			http.Error(w, "invalid node request", 400)
			return
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			http.Error(w, "trailing node request", 400)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		var reply nodeReply
		var err error
		if req.Endpoint != cfg.Endpoint {
			err = fmt.Errorf("node serves another containerd endpoint")
		} else {
			switch req.Action {
			case "validate":
				err = g.Validate(ctx, req.Runtime)
				reply.Info = g.nodeInfo(socket)
			case "inventory":
				reply.Inventory, err = g.Inventory(ctx)
			case "profiles":
				reply.Profiles = g.resourceProfiles()
			case "recover-orphan":
				err = g.RecoverOrphan(ctx, req.Allocation, req.Force)
			case "reserve":
				reply.Parent, err = g.ReserveProfile(ctx, req.Allocation, req.ResourceProfile)
			case "verify-runtime":
				err = g.VerifyRuntime(ctx, req.Runtime, req.Allocation, req.Worker)
				reply.Info = g.nodeInfo(socket)
			case "verify":
				err = g.Verify(ctx, req.Allocation, req.Worker)
			case "release":
				err = g.Release(ctx, req.Allocation, req.Worker)
			case "launch-claim", "launch-finish", "launch-abort":
				launchMu.Lock()
				switch req.Action {
				case "launch-claim":
					reply.Launch, err = g.claimLaunch(ctx, backend, req.RequestKey, req.Fingerprint)
				case "launch-finish":
					reply.Launch, err = g.finishLaunch(ctx, backend, req.RequestKey, req.Lease, req.WorkerID)
				case "launch-abort":
					reply.Launch, err = g.abortLaunch(req.RequestKey, req.Lease, req.WorkerID)
				}
				launchMu.Unlock()
			case "launch-status":
				launchMu.Lock()
				reply.Launch, err = g.readLaunch(req.RequestKey)
				// A status query never transfers the active launch lease.
				reply.Launch.Lease = ""
				launchMu.Unlock()
			default:
				err = fmt.Errorf("unknown node action")
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			reply.Error = err.Error()
			switch {
			case errors.Is(err, os.ErrNotExist):
				reply.Code = "request_not_found"
			case errors.Is(err, ErrLaunchConflict):
				reply.Code = "request_conflict"
			case errors.Is(err, ErrCapacity):
				reply.Code = "capacity_exhausted"
			}
			w.WriteHeader(http.StatusConflict)
		}
		_ = json.NewEncoder(w).Encode(reply)
	})
	stop := context.AfterFunc(ctx, func() { _ = server.Close() })
	defer stop()
	err = server.Serve(listener)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (g *Guard) nodeInfo(socket string) NodeInfo {
	info := NodeInfo{ProtocolVersion: 2, Profile: Profile, Endpoint: g.config.Endpoint, Stamp: g.Stamp(), Launcher: g.Launcher(), Root: g.config.CgroupRoot, SharedFSCache: "none", BuildVersion: buildinfo.Version, Capabilities: []string{"verify-runtime-v1", "checkpoint-retention-v1", "disk-checkpoint-v1", "request-status-v1", "request-cleanup-v1", "resource-profiles-v1", "operator-resource-profiles-v1", "filesystem-policy-v1", "idle-activity-v1", "idle-safe-v1", "create-before-start-v1", "exec-options-v1", "background-jobs-v1", "exec-pty-resize-v1", "image-digest-v1", "worker-quarantine-v1"}, Protected: append(g.ProtectedPaths(), socket)}
	if g.temporaryStorage {
		info.Capabilities = append(info.Capabilities, "bounded-tmpfs-v1")
	}
	if g.agentState && g.manifest.Assets["mkfs"].Path != "" {
		info.Capabilities = append(info.Capabilities, "agent-state-disk-v1")
	}
	if g.supportsPIDProfiles() {
		info.Capabilities = append(info.Capabilities, "dynamic-pid-profiles-v1")
	}
	return info
}

type Client struct {
	client           *http.Client
	endpoint, socket string
	mu               sync.RWMutex
	info             NodeInfo
}

func Connect(socket, endpoint string) (*Client, error) {
	canonical, err := filepath.EvalSymlinks(socket)
	if err != nil {
		return nil, fmt.Errorf("kata node service is unavailable: %w", err)
	}
	if err = trustedPath(filepath.Dir(canonical), true); err != nil {
		return nil, err
	}
	info, err := os.Lstat(canonical)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSocket == 0 || info.Mode().Perm()&0007 != 0 || !rootOwner(info) {
		return nil, fmt.Errorf("untrusted Kata node socket")
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", canonical)
	}}
	return &Client{endpoint: endpoint, socket: canonical, client: &http.Client{Transport: transport, Timeout: 35 * time.Second}}, nil
}

func (c *Client) call(ctx context.Context, req nodeRequest) (nodeReply, error) {
	var result nodeReply
	req.Endpoint = c.endpoint
	b, err := json.Marshal(req)
	if err != nil {
		return result, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://kata-node/v1/node", bytes.NewReader(b))
	if err != nil {
		return result, err
	}
	r.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(r)
	if err != nil {
		return result, fmt.Errorf("kata node service: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err = json.NewDecoder(io.LimitReader(resp.Body, 1024*1024)).Decode(&result); err != nil {
		return result, err
	}
	if resp.StatusCode != 200 || result.Error != "" {
		return result, &NodeError{Code: result.Code, Message: result.Error}
	}
	return result, nil
}

// NodeError carries a stable error code for public API clients. Empty Code
// means an older node or a failure without a specific classification.
type NodeError struct {
	Code    string
	Message string
}

func (e *NodeError) Error() string { return "kata node: " + e.Message }

type VersionMismatchError struct {
	ClientVersion string
	NodeVersion   string
}

func (e *VersionMismatchError) Error() string {
	return fmt.Sprintf("CLI/node versions differ (%s / %s); update the matching node bundle with oaiso setup", e.ClientVersion, e.NodeVersion)
}

func (c *Client) Validate(ctx context.Context, runtime string) error {
	r, err := c.call(ctx, nodeRequest{Action: "validate", Runtime: runtime})
	if err != nil {
		return err
	}
	return c.acceptInfo(r.Info)
}

func (c *Client) acceptInfo(info NodeInfo) error {
	if info.Profile != Profile || info.Endpoint != c.endpoint || len(info.Stamp) != 64 || !cleanPath(info.Root) || !cleanPath(info.Launcher) {
		return fmt.Errorf("kata node returned another profile")
	}
	if buildinfo.Version != "dev" && info.BuildVersion != buildinfo.Version {
		return &VersionMismatchError{ClientVersion: buildinfo.Version, NodeVersion: info.BuildVersion}
	}
	if info.SharedFSCache != "none" {
		return fmt.Errorf("kata node does not enforce coherent shared workspaces; update the node, regenerate its config with virtio_fs_cache=\"none\" and reseal the asset manifest")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.info.Stamp != "" && info.Stamp != c.info.Stamp {
		return fmt.Errorf("kata node profile changed; recreate manager/workers")
	}
	c.info = info
	return nil
}

func (c *Client) ProtocolVersion() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.info.ProtocolVersion
}
func (c *Client) Capabilities() (string, []string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.info.BuildVersion, append([]string(nil), c.info.Capabilities...)
}
func (c *Client) Reserve(ctx context.Context, id string) (string, error) {
	return c.ReserveProfile(ctx, id, "standard")
}

func (c *Client) ClaimLaunch(ctx context.Context, key, fingerprint string) (LaunchRecord, error) {
	r, err := c.call(ctx, nodeRequest{Action: "launch-claim", RequestKey: key, Fingerprint: fingerprint})
	return r.Launch, err
}

func (c *Client) LaunchStatus(ctx context.Context, key string) (LaunchRecord, error) {
	r, err := c.call(ctx, nodeRequest{Action: "launch-status", RequestKey: key})
	return r.Launch, err
}

func (c *Client) Profiles(ctx context.Context) ([]ResourceProfile, error) {
	r, err := c.call(ctx, nodeRequest{Action: "profiles"})
	return r.Profiles, err
}

func (c *Client) FinishLaunch(ctx context.Context, key, lease, workerID string) (LaunchRecord, error) {
	r, err := c.call(ctx, nodeRequest{Action: "launch-finish", RequestKey: key, Lease: lease, WorkerID: workerID})
	return r.Launch, err
}

func (c *Client) AbortLaunch(ctx context.Context, key, lease, workerID string) error {
	_, err := c.call(ctx, nodeRequest{Action: "launch-abort", RequestKey: key, Lease: lease, WorkerID: workerID})
	return err
}

func (c *Client) ReserveProfile(ctx context.Context, id, profile string) (string, error) {
	r, err := c.call(ctx, nodeRequest{Action: "reserve", Allocation: id, ResourceProfile: profile})
	return r.Parent, err
}
func (c *Client) Verify(ctx context.Context, id, worker string) error {
	_, err := c.call(ctx, nodeRequest{Action: "verify", Allocation: id, Worker: worker})
	return err
}

// VerifyRuntime performs full pinned-asset and host verification once per
// operation. Older nodes retain the separate validation and verification path.
func (c *Client) VerifyRuntime(ctx context.Context, runtime, id, worker string) error {

	c.mu.RLock()
	known := c.info.Stamp != ""
	supported := false
	for _, feature := range c.info.Capabilities {
		if feature == "verify-runtime-v1" {
			supported = true
			break
		}
	}
	c.mu.RUnlock()
	if supported || !known {
		r, err := c.call(ctx, nodeRequest{Action: "verify-runtime", Runtime: runtime, Allocation: id, Worker: worker})
		if err == nil {
			return c.acceptInfo(r.Info)
		}
		// A fresh manager has no capability metadata yet. Old nodes explicitly
		// reject this action before performing verification; retain their full path.
		// Never downgrade a known node or retry an attestation/transport failure.
		var nodeErr *NodeError
		if known || !errors.As(err, &nodeErr) || nodeErr.Code != "" || nodeErr.Message != "unknown node action" {
			return err
		}
	}
	if err := c.Validate(ctx, runtime); err != nil {
		return err
	}
	return c.Verify(ctx, id, worker)
}

func (c *Client) Release(ctx context.Context, id, worker string) error {
	_, err := c.call(ctx, nodeRequest{Action: "release", Allocation: id, Worker: worker})
	return err
}
func (c *Client) Parent(id string) (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" {
		return "", fmt.Errorf("invalid Kata allocation id")
	}
	return strings.TrimPrefix(filepath.Join(c.info.Root, "cdx-"+id), "/sys/fs/cgroup"), nil
}
func (c *Client) ProtectedPaths() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append(append([]string(nil), c.info.Protected...), c.socket)
}
func (c *Client) Launcher() string { c.mu.RLock(); defer c.mu.RUnlock(); return c.info.Launcher }
func (c *Client) Stamp() string    { c.mu.RLock(); defer c.mu.RUnlock(); return c.info.Stamp }
func (c *Client) Close() error     { c.client.CloseIdleConnections(); return nil }

func (c *Client) Inventory(ctx context.Context) (Inventory, error) {
	r, err := c.call(ctx, nodeRequest{Action: "inventory"})
	return r.Inventory, err
}
func (c *Client) RecoverOrphan(ctx context.Context, id string, force bool) error {
	_, err := c.call(ctx, nodeRequest{Action: "recover-orphan", Allocation: id, Force: force})
	return err
}
