package cmd

import (
	"context"
	"crypto/x509"
	"fmt"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	dn "github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
	"github.com/pacificbelt30/codex-dock/internal/authproxy"
	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/pkg/cxd/credentialstore"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const proxyCredentialHome = "/var/lib/codex-dock-proxy"
const proxyProfileLabel = "io.codex-dock.auth-profile"

func profileProxyID(a proxyRunArgs) string {
	if a.profileName == "" {
		return ""
	}
	return caseDigest(a.profileName + "\x00" + a.profileDirectory + "\x00" + a.profileConnection)
}

func hasProfileMount(host *container.HostConfig, directory string) bool {
	for _, m := range host.Mounts {
		if m.Type == mount.TypeBind && m.Source == directory && m.Target == "/run/cxd-profile" && !m.ReadOnly {
			return true
		}
	}
	return false
}

// The proxy spec has one source of truth for credentials, policy, and roles.
// Convert its structured option vector to the node DTO; no shell/CLI is run.
func proxyConfiguration(a proxyRunArgs) (*container.Config, *container.HostConfig, error) {
	if a.usageStateDir != "" {
		if a.role != authproxy.RoleAuth || !filepath.IsAbs(a.usageStateDir) || filepath.Clean(a.usageStateDir) != a.usageStateDir {
			return nil, nil, fmt.Errorf("usage state requires an absolute private auth-proxy directory")
		}
		if err := os.MkdirAll(a.usageStateDir, 0700); err != nil {
			return nil, nil, err
		}
		info, err := os.Lstat(a.usageStateDir)
		if err != nil {
			return nil, nil, err
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || owner.Uid != uint32(os.Geteuid()) || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			return nil, nil, fmt.Errorf("usage state must be owner-private (0700)")
		}
	}
	args := buildProxyRunArgs(a)
	c := &container.Config{OpenStdin: a.profileFingerprint != "", User: "0:0", Labels: map[string]string{}}
	h := &container.HostConfig{Runtime: "runc", CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges:true"}, PortBindings: nat.PortMap{}}
	if a.usageStateDir != "" {
		c.User = fmt.Sprintf("%d:%d", os.Geteuid(), os.Getegid())
	}
	if a.brokerFile != "" {
		if a.role != "auth" {
			return nil, nil, fmt.Errorf("credential broker routes require auth role")
		}
		if _, err := authproxy.LoadCredentialRoutes(a.brokerFile); err != nil {
			return nil, nil, err
		}
		c.User = fmt.Sprintf("%d:%d", os.Geteuid(), os.Getegid())
	}
	if a.caFile != "" {
		info, err := os.Stat(a.caFile)
		if err != nil {
			return nil, nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, nil, fmt.Errorf("proxy CA bundle must be a regular file")
		}
		if info.Size() > 16<<20 {
			return nil, nil, fmt.Errorf("proxy CA bundle exceeds 16 MiB")
		}
		bundle, err := os.ReadFile(a.caFile)
		if err != nil {
			return nil, nil, err
		}
		if !x509.NewCertPool().AppendCertsFromPEM(bundle) {
			return nil, nil, fmt.Errorf("proxy CA bundle has no certificates")
		}
	}
	if a.mountCreds {
		// Read owner-only host credentials as the invoking account, without
		// granting DAC override or changing the source file's permissions.
		c.User = fmt.Sprintf("%d:%d", os.Geteuid(), os.Getegid())
		c.Env = append(c.Env, "HOME="+proxyCredentialHome)
	}
	if a.credentialSocket != "" {
		c.User = fmt.Sprintf("%d:%d", os.Geteuid(), os.Getegid())
	}
	if a.adminSecretFile != "" {
		if a.role != authproxy.RoleAuth {
			return nil, nil, fmt.Errorf("admin secret file belongs only to the auth proxy")
		}
		if _, err := resolveProxyAdminSecret("", a.adminSecretFile); err != nil {
			return nil, nil, err
		}
		c.User = fmt.Sprintf("%d:%d", os.Geteuid(), os.Getegid())
	}
	if a.profileName != "" {
		if a.role != authproxy.RoleAuth || a.mountCreds || a.credentialSocket != "" {
			return nil, nil, fmt.Errorf("independent profile requires exclusive auth-role storage")
		}
		c.User = fmt.Sprintf("%d:%d", os.Geteuid(), os.Getegid())
	}
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "-d" {
			continue
		}
		if arg == a.image {
			c.Image = a.image
			c.Cmd = append([]string{}, args[i+1:]...)
			return c, h, nil
		}
		if i+1 >= len(args) {
			return nil, nil, fmt.Errorf("invalid proxy spec")
		}
		i++
		value := args[i]
		switch arg {
		case "--name":
		case "--label":
			k, v, ok := strings.Cut(value, "=")
			if !ok {
				return nil, nil, fmt.Errorf("invalid proxy label")
			}
			c.Labels[k] = v
		case "--network":
			h.NetworkMode = container.NetworkMode(value)
		case "-e":
			c.Env = append(c.Env, value)
		case "-v":
			parts := strings.Split(value, ":")
			if len(parts) == 3 && parts[1] == "/run/cxd-usage" && parts[2] == "rw" && parts[0] == a.usageStateDir {
				h.Mounts = append(h.Mounts, mount.Mount{Type: mount.TypeBind, Source: parts[0], Target: parts[1]})
				continue
			}
			if len(parts) == 3 && parts[1] == "/run/cxd-admin-secret" && parts[2] == "ro" {
				source, err := filepath.Abs(parts[0])
				if err != nil {
					return nil, nil, err
				}
				h.Mounts = append(h.Mounts, mount.Mount{Type: mount.TypeBind, Source: source, Target: parts[1], ReadOnly: true})
				continue
			}
			if len(parts) == 3 && parts[1] == "/run/cxd-profile" && parts[2] == "rw" {
				store, err := credentialstore.OpenProfile(parts[0], a.profileName)
				if err != nil {
					return nil, nil, err
				}
				if encrypted, ok := store.(*credentialstore.EncryptedStore); ok {
					defer func() { _ = encrypted.Close() }()
					info, err := encrypted.Info(context.Background())
					if err != nil {
						return nil, nil, err
					}
					if info.Fingerprint != a.profileFingerprint {
						return nil, nil, fmt.Errorf("encrypted profile key changed before proxy creation")
					}
					if err = encrypted.UnlockWithKey(context.Background(), a.profileKey); err != nil {
						return nil, nil, err
					}
				}
				loaded, err := store.Load(context.Background(), a.profileName)
				if err != nil {
					return nil, nil, err
				}
				if loaded.ConnectionVersion != a.profileConnection {
					return nil, nil, fmt.Errorf("profile connection changed before proxy creation")
				}
				h.Mounts = append(h.Mounts, mount.Mount{Type: mount.TypeBind, Source: store.Directory(), Target: parts[1]})
				continue
			}
			if len(parts) == 3 && (parts[1] == "/run/cxd-proxy-ca.pem" || parts[1] == "/run/cxd-broker.json") && parts[2] == "ro" {
				source, err := filepath.Abs(parts[0])
				if err != nil {
					return nil, nil, err
				}
				h.Mounts = append(h.Mounts, mount.Mount{Type: mount.TypeBind, Source: source, Target: parts[1], ReadOnly: true})
				continue
			}
			if len(parts) == 3 && parts[1] == "/run/cxd-credential-store.sock" && parts[2] == "ro" {
				info, err := os.Lstat(parts[0])
				if err != nil {
					return nil, nil, err
				}
				owner, ok := info.Sys().(*syscall.Stat_t)
				if !ok || owner.Uid != uint32(os.Geteuid()) || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm()&0077 != 0 {
					return nil, nil, fmt.Errorf("credential store socket must be private")
				}
				h.Mounts = append(h.Mounts, mount.Mount{Type: mount.TypeBind, Source: parts[0], Target: parts[1], ReadOnly: true})
				continue
			}
			if len(parts) != 3 || (parts[2] != "ro" && (parts[2] != "rw" || parts[1] != "/root/.local/state/codex-dock/oauth")) {
				return nil, nil, fmt.Errorf("invalid proxy bind mount mode")
			}
			if !strings.HasPrefix(parts[1], "/root/") {
				return nil, nil, fmt.Errorf("invalid proxy credential destination")
			}
			if parts[2] == "ro" {
				if err := checkProxyCredentialAccess(parts[0], os.Geteuid(), os.Getegid()); err != nil {
					return nil, nil, err
				}
			} else {
				info, err := os.Lstat(parts[0])
				if err != nil {
					return nil, nil, err
				}
				if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
					return nil, nil, fmt.Errorf("OAuth state directory must be private")
				}
			}
			target := filepath.Join(proxyCredentialHome, strings.TrimPrefix(parts[1], "/root/"))
			h.Mounts = append(h.Mounts, mount.Mount{Type: mount.TypeBind, Source: parts[0], Target: target, ReadOnly: parts[2] == "ro"})
		case "-p":
			parts := strings.Split(value, ":")
			if len(parts) != 3 || parts[0] != "127.0.0.1" {
				return nil, nil, fmt.Errorf("proxy admin must publish on loopback")
			}
			if _, err := strconv.Atoi(parts[1]); err != nil {
				return nil, nil, err
			}
			p, err := nat.NewPort("tcp", parts[2])
			if err != nil {
				return nil, nil, err
			}
			h.PortBindings[p] = []nat.PortBinding{{HostIP: parts[0], HostPort: parts[1]}}
		default:
			return nil, nil, fmt.Errorf("unsupported proxy option %q", arg)
		}
	}
	return nil, nil, fmt.Errorf("proxy spec has no image")
}

func checkProxyCredentialAccess(path string, uid, gid int) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("proxy credential metadata: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() {
		return fmt.Errorf("proxy credential must be a regular file: %s", path)
	}
	permission := os.FileMode(0004)
	if uint32(uid) == stat.Uid {
		permission = 0400
	} else if uint32(gid) == stat.Gid {
		permission = 0040
	}
	if info.Mode().Perm()&permission == 0 {
		return fmt.Errorf("proxy credential %s is not readable by proxy UID:GID %d:%d without capabilities; run as its owning account", path, uid, gid)
	}
	return nil
}
func stopProxy(ctx context.Context, name string) error {
	c, err := engine.NewClient()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	return c.ContainerStop(ctx, name, container.StopOptions{})
}
func removeProxy(ctx context.Context, name string, force bool) error {
	c, err := engine.NewClient()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	return c.ContainerRemove(ctx, name, container.RemoveOptions{Force: force})
}
func ensureNodeBridge(ctx context.Context, name string) error {
	c, err := engine.NewClient()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	_, err = c.NetworkInspect(ctx, name, dn.InspectOptions{})
	if err == nil {
		return nil
	}
	_, err = c.NetworkCreate(ctx, name, dn.CreateOptions{Driver: "bridge", Labels: map[string]string{"codex-dock.managed": "true"}})
	return err
}
