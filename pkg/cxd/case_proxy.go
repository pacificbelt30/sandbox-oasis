package cxd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	dn "github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
	"github.com/pacificbelt30/codex-dock/internal/authproxy"
	"github.com/pacificbelt30/codex-dock/internal/buildinfo"
	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/netquota"
	"github.com/pacificbelt30/codex-dock/internal/network"
)

// ProxyResourceLimits bounds proxy TCP connections and per-source bandwidth.
type ProxyResourceLimits = netquota.Limits

// CaseProxyOptions creates a distinct auth proxy for one credential case.
// StoreSocket is served for CaseID by credentialstore.Serve in the trusted host product.
type CaseProxyOptions struct {
	NetworkResources ProxyResourceLimits
	NodeSocket       string
	Image            string
	Name             string
	CaseID           string
	StoreSocket      string
	AdminPort        int
	AdminSecret      string
	// AllowedModels optionally enforces exact upstream model IDs for this case.
	AllowedModels []string
	// UsageStateDir optionally mounts owner-private durable counters into this
	// auth proxy only. Required before requesting per-execution usage limits.
	UsageStateDir string
}

type CaseProxy struct {
	Name         string
	AdminURL     string
	ContainerURL string
}

var caseProxyName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

// EnsureCaseProxy starts an auth proxy with no host HOME credential mounts.
// The store socket is mounted only into this proxy, never into worker VMs.
func EnsureCaseProxy(ctx context.Context, opts CaseProxyOptions) (CaseProxy, error) {
	if err := opts.NetworkResources.Validate(); err != nil {
		return CaseProxy{}, err
	}
	if opts.UsageStateDir != "" {
		if !filepath.IsAbs(opts.UsageStateDir) || filepath.Clean(opts.UsageStateDir) != opts.UsageStateDir {
			return CaseProxy{}, fmt.Errorf("usage directory must be a clean absolute path")
		}
		if err := os.MkdirAll(opts.UsageStateDir, 0700); err != nil {
			return CaseProxy{}, err
		}
		info, err := os.Lstat(opts.UsageStateDir)
		if err != nil {
			return CaseProxy{}, err
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != uint32(os.Geteuid()) || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			return CaseProxy{}, fmt.Errorf("usage directory must be owner-private (0700)")
		}
	}
	if err := authproxy.ValidateAllowedModels(opts.AllowedModels); err != nil {
		return CaseProxy{}, err
	}
	if !caseProxyName.MatchString(opts.Name) || opts.CaseID == "" || len(opts.CaseID) > 256 || strings.ContainsAny(opts.CaseID, "\x00\r\n") || len(opts.AdminSecret) < 32 || opts.AdminPort < 1 || opts.AdminPort > 65535 {
		return CaseProxy{}, fmt.Errorf("case proxy requires name, case ID, admin secret and a valid admin port")
	}
	if !filepath.IsAbs(opts.StoreSocket) || filepath.Clean(opts.StoreSocket) != opts.StoreSocket {
		return CaseProxy{}, fmt.Errorf("credential store socket must be a clean absolute path")
	}
	info, err := os.Lstat(opts.StoreSocket)
	if err != nil {
		return CaseProxy{}, err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm()&0077 != 0 {
		return CaseProxy{}, fmt.Errorf("credential store socket must be private")
	}
	if opts.NodeSocket == "" {
		opts.NodeSocket = engine.Socket()
	}
	if opts.Image == "" {
		opts.Image = "codex-dock-proxy:latest"
	}
	cli, err := engine.NewClientAt(opts.NodeSocket)
	if err != nil {
		return CaseProxy{}, err
	}
	defer func() { _ = cli.Close() }()
	imageInfo, err := cli.ImageInspect(ctx, opts.Image)
	if err != nil {
		return CaseProxy{}, err
	}
	if imageInfo.Config == nil || imageInfo.Config.Labels["io.codex-dock.proxy-protocol"] != "external-store-v1" || imageInfo.Config.Labels["io.codex-dock.proxy-build"] != buildinfo.Version {
		return CaseProxy{}, fmt.Errorf("case proxy image is incompatible with Go client; rebuild the proxy image")
	}
	if existing, err := cli.NetworkInspect(ctx, network.EgressNetworkName, dn.InspectOptions{}); err == nil {
		if existing.Driver != "bridge" || existing.Labels["codex-dock.managed"] != "true" {
			return CaseProxy{}, fmt.Errorf("egress network is not managed by cxd")
		}
	} else if errdefs.IsNotFound(err) {
		_, err = cli.NetworkCreate(ctx, network.EgressNetworkName, dn.CreateOptions{Driver: "bridge", Options: map[string]string{"com.docker.network.bridge.name": network.EgressBridgeName}, Labels: map[string]string{"codex-dock.managed": "true"}})
		if err != nil {
			return CaseProxy{}, err
		}
	} else {
		return CaseProxy{}, err
	}
	sum := sha256.Sum256([]byte(opts.CaseID))
	caseHash := hex.EncodeToString(sum[:])
	result := CaseProxy{Name: opts.Name, AdminURL: "http://127.0.0.1:" + strconv.Itoa(opts.AdminPort), ContainerURL: (&url.URL{Scheme: "https", Host: net.JoinHostPort(opts.Name, "18080")}).String()}
	if existing, err := cli.ContainerInspect(ctx, opts.Name); err == nil {
		if existing.Config == nil || existing.State == nil || existing.Config.Image != opts.Image || existing.Config.Labels["io.codex-dock.proxy-layout"] != "split-v4-worker-mtls" || existing.Config.Labels["io.codex-dock.credential-case"] != caseHash {
			return CaseProxy{}, fmt.Errorf("case proxy name already belongs to another image, layout or case")
		}
		if existing.Config.Labels[netquota.Label] != opts.NetworkResources.ID() {
			return CaseProxy{}, fmt.Errorf("case proxy network resource limits differ; drain workers before replacement")
		}
		mounted := false
		usageDirectory := ""
		if existing.HostConfig != nil {
			for _, m := range existing.HostConfig.Mounts {
				if m.Target == "/run/cxd-usage" && !m.ReadOnly {
					usageDirectory = m.Source
				}
				if m.Source == opts.StoreSocket && m.Target == "/run/cxd-credential-store.sock" && m.ReadOnly {
					mounted = true
				}
			}
		}
		if usageDirectory != opts.UsageStateDir {
			return CaseProxy{}, fmt.Errorf("case proxy usage directory differs; drain workers before replacement")
		}
		if !sameModelPolicy(existing.Config.Cmd, opts.AllowedModels) {
			return CaseProxy{}, fmt.Errorf("case proxy model policy differs; drain workers before replacement")
		}
		if !mounted {
			return CaseProxy{}, fmt.Errorf("case proxy credential socket differs; drain workers before replacement")
		}
		port, err := nat.NewPort("tcp", strconv.Itoa(opts.AdminPort))
		if err != nil {
			return CaseProxy{}, err
		}
		bindings := existing.HostConfig.PortBindings[port]
		if len(bindings) != 1 || bindings[0].HostIP != "127.0.0.1" || bindings[0].HostPort != strconv.Itoa(opts.AdminPort) {
			return CaseProxy{}, fmt.Errorf("case proxy admin port differs; drain workers before replacement")
		}
		if !existing.State.Running {
			if err := cli.ContainerStart(ctx, opts.Name, container.StartOptions{}); err != nil {
				return CaseProxy{}, err
			}
		}
		return result, waitCaseProxy(ctx, result, opts.AdminSecret)
	} else if !errdefs.IsNotFound(err) {
		return CaseProxy{}, err
	}
	port, err := nat.NewPort("tcp", strconv.Itoa(opts.AdminPort))
	if err != nil {
		return CaseProxy{}, err
	}
	cfg := &container.Config{
		Image: opts.Image, User: fmt.Sprintf("%d:%d", os.Geteuid(), os.Getegid()),
		Cmd:    []string{"proxy", "serve", "--worker-mtls", "--listen", "0.0.0.0:18080", "--role", "auth", "--admin-listen", "egress:" + strconv.Itoa(opts.AdminPort), "--admin-secret", opts.AdminSecret, "--block-private", "--credential-store-socket", "/run/cxd-credential-store.sock", "--case-id", opts.CaseID},
		Labels: map[string]string{"io.codex-dock.proxy-layout": "split-v4-worker-mtls", "io.codex-dock.credential-case": caseHash},
	}
	if id := opts.NetworkResources.ID(); id != "" {
		cfg.Labels[netquota.Label] = id
	}
	cfg.Cmd = append(cfg.Cmd, opts.NetworkResources.Args()...)
	for _, model := range opts.AllowedModels {
		cfg.Cmd = append(cfg.Cmd, "--allow-model", model)
	}
	host := &container.HostConfig{Runtime: "runc", CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges:true"}, NetworkMode: container.NetworkMode(network.EgressNetworkName), Mounts: []mount.Mount{{Type: mount.TypeBind, Source: opts.StoreSocket, Target: "/run/cxd-credential-store.sock", ReadOnly: true}}, PortBindings: nat.PortMap{port: []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: strconv.Itoa(opts.AdminPort)}}}}
	if opts.UsageStateDir != "" {
		cfg.Cmd = append(cfg.Cmd, "--usage-state-dir", "/run/cxd-usage")
		host.Mounts = append(host.Mounts, mount.Mount{Type: mount.TypeBind, Source: opts.UsageStateDir, Target: "/run/cxd-usage"})
	}
	created, err := cli.ContainerCreate(ctx, cfg, host, nil, nil, opts.Name)
	if err != nil {
		return CaseProxy{}, err
	}
	if err := cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return CaseProxy{}, err
	}
	return result, waitCaseProxy(ctx, result, opts.AdminSecret)
}

func waitCaseProxy(ctx context.Context, proxy CaseProxy, secret string) error {
	waitCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for {
		if remote, err := authproxy.NewRemoteProxyContext(waitCtx, proxy.AdminURL, proxy.ContainerURL, secret); err == nil && remote.IsCaseProxy() {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("case proxy did not become ready: %w", waitCtx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// RemoveCaseProxy removes a case proxy after every worker that references it
// has been removed. Its case digest is checked before any destructive action.
func RemoveCaseProxy(ctx context.Context, nodeSocket, name, caseID string) error {
	if !caseProxyName.MatchString(name) || caseID == "" {
		return fmt.Errorf("invalid case proxy identity")
	}
	if nodeSocket == "" {
		nodeSocket = engine.Socket()
	}
	cli, err := engine.NewClientAt(nodeSocket)
	if err != nil {
		return err
	}
	defer func() { _ = cli.Close() }()
	v, err := cli.ContainerInspect(ctx, name)
	if errdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(caseID))
	if v.Config == nil || v.Config.Labels["io.codex-dock.proxy-layout"] != "split-v4-worker-mtls" || v.Config.Labels["io.codex-dock.credential-case"] != hex.EncodeToString(sum[:]) {
		return fmt.Errorf("proxy %q does not belong to this credential case", name)
	}
	workers, err := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: filters.NewArgs(filters.Arg("label", "codex-dock.managed=true"))})
	if err != nil {
		return err
	}
	endpoint := "http://" + net.JoinHostPort(name, "18080")
	for _, worker := range workers {
		if worker.Labels["codex-dock.proxy-container-url"] == endpoint {
			return fmt.Errorf("proxy %q still has a retained worker", name)
		}
	}
	return cli.ContainerRemove(ctx, name, container.RemoveOptions{Force: true})
}

// Compare immutable launch arguments, including absent flags on legacy proxies.
func sameModelPolicy(args, models []string) bool {
	actual := []string{}
	for i := 0; i < len(args); i++ {
		if args[i] == "--allow-model" {
			i++
			if i == len(args) {
				return false
			}
			actual = append(actual, args[i])
		} else if strings.HasPrefix(args[i], "--allow-model=") {
			actual = append(actual, strings.TrimPrefix(args[i], "--allow-model="))
		}
	}
	return slices.Equal(actual, models)
}
