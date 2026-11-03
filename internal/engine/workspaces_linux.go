//go:build linux

package engine

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/containerd/errdefs"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pacificbelt30/codex-dock/internal/managedworkspace"
	"github.com/pacificbelt30/codex-dock/internal/privatefile"
	"github.com/pacificbelt30/codex-dock/internal/strictjson"
	"github.com/pacificbelt30/codex-dock/internal/workerpolicy"
	"golang.org/x/sys/unix"
)

type workspaceRecord struct {
	Version       int
	Info          managedworkspace.Info
	Device, Inode uint64
}

func (b *Backend) workspaceRecords() string { return filepath.Join(b.opts.State, "managed-workspaces") }
func (b *Backend) workspaceData() string {
	return filepath.Join(b.opts.StorageRoot, "oaiso-workspaces")
}
func (b *Backend) workspacePath(id string) string { return filepath.Join(b.workspaceData(), id) }
func (b *Backend) workspaceImage(id string) string {
	return filepath.Join(b.workspacePath(id), "disk.ext4")
}
func (b *Backend) workspaceSource(id string) string {
	return filepath.Join(b.workspacePath(id), "mount")
}
func (b *Backend) workspaceMetadata(id string) string {
	return filepath.Join(b.stateVolumeRoot(), base64.URLEncoding.EncodeToString([]byte(b.workspaceSource(id))))
}
func privateDirectory(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	return existingPrivateDirectory(path)
}
func existingPrivateDirectory(path string) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	s, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.IsDir() || st.Mode().Perm() != 0700 || s.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("managed workspace directory must be owner-private")
	}
	return nil
}
func saveWorkspaceJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) > privatefile.Limit {
		return fmt.Errorf("managed workspace metadata exceeds 1 MiB")
	}
	if err = privateDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".workspace-")
	if err != nil {
		return err
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	return syncStateDirectory(filepath.Dir(path))
}
func (b *Backend) saveWorkspace(r *workspaceRecord) error {
	return saveWorkspaceJSON(filepath.Join(b.workspaceRecords(), r.Info.ID+".json"), r)
}
func (b *Backend) loadWorkspace(id string) (*workspaceRecord, error) {
	if !managedworkspace.ID.MatchString(id) {
		return nil, fmt.Errorf("invalid managed workspace ID: %w", errdefs.ErrInvalidArgument)
	}
	data, err := privatefile.Read(filepath.Join(b.workspaceRecords(), id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, errdefs.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var r workspaceRecord
	if err = strictjson.Decode(strings.NewReader(string(data)), privatefile.Limit, &r); err != nil {
		return nil, err
	}
	if r.Version != 1 || r.Info.ID != id || r.Info.Options.Validate() != nil || r.Info.Created.IsZero() || r.Info.Kind != "workspace" && r.Info.Kind != "snapshot" || r.Info.Lease != "" && !hexID.MatchString(r.Info.Lease) || r.Info.Operation != "" && !hexID.MatchString(r.Info.Operation) {
		return nil, fmt.Errorf("managed workspace identity or ownership record is invalid")
	}
	switch r.Info.State {
	case "empty", "ready", "saving", "deleting", "failed", "restoring":
	default:
		return nil, fmt.Errorf("unknown managed workspace state")
	}
	return &r, nil
}
func (b *Backend) workspaceCatalog() (managedworkspace.Catalog, error) {
	out := managedworkspace.Catalog{CapacityBytes: managedworkspace.CapacityBytes, Items: []managedworkspace.Info{}}
	if err := privateDirectory(b.workspaceRecords()); err != nil {
		return out, err
	}
	dir, err := os.Open(b.workspaceRecords())
	if err != nil {
		return out, err
	}
	defer func() { _ = dir.Close() }()
	for {
		entries, e := dir.ReadDir(64)
		if e != nil && e != io.EOF {
			return out, e
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".workspace-") {
				continue
			}
			if strings.HasSuffix(entry.Name(), ".seed") && managedworkspace.ID.MatchString(strings.TrimSuffix(entry.Name(), ".seed")) {
				continue
			}
			if !strings.HasSuffix(entry.Name(), ".json") || entry.IsDir() {
				return out, fmt.Errorf("unexpected private workspace metadata")
			}
			r, err := b.loadWorkspace(strings.TrimSuffix(entry.Name(), ".json"))
			if err != nil {
				return out, err
			}
			out.Items = append(out.Items, r.Info)
			out.ReservedBytes += r.Info.Options.Bytes
			if len(out.Items) > managedworkspace.MaxItems {
				return out, fmt.Errorf("workspace catalog exceeds its bound")
			}
		}
		if e == io.EOF {
			break
		}
	}
	sort.Slice(out.Items, func(i, j int) bool { return out.Items[i].Created.After(out.Items[j].Created) })
	return out, nil
}
func (b *Backend) admitWorkspace(ctx context.Context, bytes int64) error {
	if b.workspaceCapacityCheck != nil {
		return b.workspaceCapacityCheck(ctx, bytes)
	}
	status := b.storageStatus()
	if !status.Enforced {
		return fmt.Errorf("managed workspaces require verified dedicated XFS storage: %s", status.Error)
	}
	if b.client == nil {
		return fmt.Errorf("containerd unavailable; workspace reservations unknown")
	}
	all, err := b.client.Containers(ctx)
	if err != nil {
		return err
	}
	reserved := uint64(bytes) + rootFSReserve
	for _, c := range all {
		r, _, err := b.load(ctx, c.ID())
		if err != nil {
			return err
		}
		if r.RootFSQuota == nil {
			continue
		}
		if _, err = c.Task(ctx, nil); errdefs.IsNotFound(err) {
			continue
		} else if err != nil {
			return err
		}
		reserved += rootFSBytes
	}
	if status.AvailableBytes < reserved {
		return fmt.Errorf("insufficient workspace capacity after active rootfs reservations and host reserve")
	}
	return nil
}
func (b *Backend) newWorkspace(ctx context.Context, options managedworkspace.Options) (*workspaceRecord, error) {
	if err := options.Validate(); err != nil {
		return nil, err
	}
	if b.opts.Tools["mkfs"] == "" || !filepath.IsAbs(b.opts.StorageRoot) {
		return nil, fmt.Errorf("managed workspace requires an updated node with dedicated storage and pinned formatter")
	}
	catalog, err := b.workspaceCatalog()
	if err != nil {
		return nil, err
	}
	if len(catalog.Items) >= managedworkspace.MaxItems || catalog.ReservedBytes+options.Bytes > managedworkspace.CapacityBytes {
		return nil, fmt.Errorf("managed workspace catalog capacity exhausted")
	}
	if err = b.admitWorkspace(ctx, options.Bytes+options.SeedLimit()); err != nil {
		return nil, err
	}
	workspaceID, err := id()
	if err != nil {
		return nil, err
	}
	r := &workspaceRecord{Version: 1, Info: managedworkspace.Info{ID: workspaceID, Options: options, State: "empty", Kind: "workspace", Created: time.Now().UTC()}}
	if err = privateDirectory(b.workspaceData()); err != nil {
		return nil, err
	}
	if err = os.Mkdir(b.workspacePath(workspaceID), 0700); err != nil {
		return nil, err
	}
	if b.client != nil {
		if err = b.protectUsageMounts(ctx, b.privateAuthorityRecord()); err != nil {
			return nil, err
		}
	}
	if err = b.saveWorkspace(r); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(b.workspaceImage(workspaceID), os.O_CREATE|os.O_EXCL|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return r, err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return r, err
	}
	s, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return r, fmt.Errorf("workspace disk stat unavailable")
	}
	r.Device, r.Inode = uint64(s.Dev), s.Ino
	if err = b.saveWorkspace(r); err != nil {
		return r, err
	}
	if err = unix.Fallocate(int(f.Fd()), 0, 0, options.Bytes); err != nil {
		return r, fmt.Errorf("reserving workspace disk %s: %w", workspaceID, err)
	}
	if err = f.Sync(); err != nil {
		return r, err
	}
	if err = os.Mkdir(b.workspaceSource(workspaceID), 0700); err != nil {
		return r, err
	}
	return r, nil
}
func (b *Backend) validateWorkspaceDisk(r *workspaceRecord) error {
	if err := existingPrivateDirectory(b.workspacePath(r.Info.ID)); err != nil {
		return err
	}
	device, inode, err := diskIdentity(b.workspaceImage(r.Info.ID), r.Info.Options.Bytes)
	if err != nil {
		return err
	}
	if device != r.Device || inode != r.Inode || device == 0 || inode == 0 {
		return fmt.Errorf("managed workspace disk identity changed; data retained")
	}
	st, err := os.Lstat(b.workspaceImage(r.Info.ID))
	if err != nil {
		return err
	}
	s, ok := st.Sys().(*syscall.Stat_t)
	if !ok || s.Blocks*512 < r.Info.Options.Bytes {
		return fmt.Errorf("workspace physical reservation is incomplete; data retained")
	}
	return nil
}
func (b *Backend) seedWorkspace(ctx context.Context, r *workspaceRecord, input io.Reader) error {
	if r.Info.State != "empty" || r.Info.Lease != "" {
		return fmt.Errorf("workspace can only be seeded before first use: %w", errdefs.ErrConflict)
	}
	if err := b.validateWorkspaceDisk(r); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(b.workspacePath(r.Info.ID), ".seed-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	manifest, err := managedworkspace.ExtractSeed(ctx, input, stage, r.Info.Options)
	if err != nil {
		return err
	}
	image := b.workspaceImage(r.Info.ID)
	command := exec.CommandContext(ctx, b.opts.Tools["mkfs"], "-q", "-t", "ext4", "-F", "-b", "4096", "-m", "0", "-N", fmt.Sprint(r.Info.Options.Inodes), "-E", fmt.Sprintf("nodiscard,root_owner=%d:%d,lazy_itable_init=0,lazy_journal_init=0", r.Info.Options.UID, r.Info.Options.GID), "-d", stage, image)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	var output workspaceOutput
	command.Stdout, command.Stderr = &output, &output
	if err = command.Run(); err != nil {
		return fmt.Errorf("formatting new workspace disk: %w: %s", err, output.data)
	}
	f, err := os.OpenFile(image, os.O_RDWR|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	// This superblock was just produced by the pinned formatter and has never
	// been exposed to a guest. Guest-written filesystems are never parsed here.
	var super [1024]byte
	if _, err = f.ReadAt(super[:], 1024); err != nil {
		return err
	}
	if binary.LittleEndian.Uint16(super[56:58]) != 0xef53 || uint64(binary.LittleEndian.Uint32(super[0:4])) > r.Info.Options.Inodes || binary.LittleEndian.Uint32(super[24:28]) != 2 || uint64(binary.LittleEndian.Uint32(super[4:8]))*4096 != uint64(r.Info.Options.Bytes) {
		return fmt.Errorf("formatter did not impose the requested workspace capacity/inode ceiling")
	}
	if err = unix.Fallocate(int(f.Fd()), 0, 0, r.Info.Options.Bytes); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = b.validateWorkspaceDisk(r); err != nil {
		return err
	}
	if err = saveWorkspaceJSON(filepath.Join(b.workspaceRecords(), r.Info.ID+".seed"), manifest); err != nil {
		return err
	}
	r.Info.SeedID, r.Info.SeedFiles, r.Info.SeedBytes = manifest.ID, len(manifest.Files), manifest.Bytes
	r.Info.State = "ready"
	return b.saveWorkspace(r)
}

type workspaceOutput struct{ data []byte }

func (w *workspaceOutput) Write(p []byte) (int, error) {
	if len(w.data)+len(p) > 64<<10 {
		return 0, fmt.Errorf("workspace operation output exceeds 64 KiB")
	}
	w.data = append(w.data, p...)
	return len(p), nil
}
func (b *Backend) workspaceManifest(r *workspaceRecord) (managedworkspace.Manifest, error) {
	var out managedworkspace.Manifest
	data, err := privatefile.Read(filepath.Join(b.workspaceRecords(), r.Info.ID+".seed"))
	if err != nil {
		return out, err
	}
	if err = strictjson.Decode(strings.NewReader(string(data)), managedworkspace.MaxManifestBytes, &out); err != nil {
		return out, err
	}
	if err = out.Validate(r.Info.Options.SeedLimit()); err != nil {
		return out, err
	}
	if out.ID != r.Info.SeedID {
		return out, fmt.Errorf("workspace seed identity changed")
	}
	return out, nil
}
func (b *Backend) workspaceMountInfo(r *workspaceRecord) directVolumeInfo {
	mode := "rw"
	if r.Info.ReadOnly {
		mode = "ro"
	}
	return directVolumeInfo{Type: "directvol", Device: b.workspaceImage(r.Info.ID), FSType: "ext4", Options: []string{mode, "nosuid", "nodev", "nodiscard", "errors=remount-ro"}}
}
func (b *Backend) ensureWorkspaceMetadata(r *workspaceRecord) error {
	if err := b.validateWorkspaceDisk(r); err != nil {
		return err
	}
	for _, p := range []string{b.workspaceSource(r.Info.ID), b.stateVolumeRoot(), b.workspaceMetadata(r.Info.ID)} {
		if err := stateDirectory(p); err != nil {
			return err
		}
	}
	path := filepath.Join(b.workspaceMetadata(r.Info.ID), "mountInfo.json")
	expected, _ := json.Marshal(b.workspaceMountInfo(r))
	if data, err := stateMetadataFile(path); err == nil {
		if string(data) != string(expected) {
			return fmt.Errorf("managed workspace runtime metadata changed")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return saveWorkspaceJSON(path, b.workspaceMountInfo(r))
}
func (b *Backend) attachManagedWorkspace(worker *record, spec *specs.Spec) error {
	workspaceID := worker.Config.Labels[managedworkspace.Label]
	if workspaceID == "" {
		return nil
	}
	b.workspaceMu.Lock()
	defer b.workspaceMu.Unlock()
	r, err := b.loadWorkspace(workspaceID)
	if err != nil {
		return err
	}
	if worker.Config.Labels["codex-dock.managed"] != "true" || worker.Host.Runtime != "io.containerd.kata.v2" || worker.Config.Labels["codex-dock.authentication-session"] == "true" || r.Info.State != "ready" || r.Info.Kind != "workspace" || r.Info.Lease != "" && r.Info.Lease != worker.ID || spec.Process.User.UID != r.Info.Options.UID || spec.Process.User.GID != r.Info.Options.GID {
		return fmt.Errorf("workspace requires an available disk and matching non-root Kata worker owner: %w", errdefs.ErrConflict)
	}
	for _, m := range spec.Mounts {
		if workerpolicy.PathsOverlap(m.Destination, "/workspace") {
			return fmt.Errorf("managed workspace conflicts with a supplied mount")
		}
	}
	if spec.Linux != nil {
		for _, p := range append(append([]string(nil), spec.Linux.MaskedPaths...), spec.Linux.ReadonlyPaths...) {
			if workerpolicy.PathsOverlap(p, "/workspace") {
				return fmt.Errorf("managed workspace requires whole-disk read-only instead of per-path masks")
			}
		}
	}
	r.Info.Lease = worker.ID
	r.Info.ReadOnly = worker.Config.Labels[managedworkspace.ReadOnlyLabel] == "true"
	if err = b.saveWorkspace(r); err != nil {
		return err
	}
	if err = b.ensureWorkspaceMetadata(r); err != nil {
		return err
	}
	mode := "rw"
	if r.Info.ReadOnly {
		mode = "ro"
	}
	spec.Mounts = append(spec.Mounts, specs.Mount{Source: b.workspaceSource(workspaceID), Destination: "/workspace", Type: "bind", Options: []string{"rbind", "rprivate", mode}})
	return nil
}
func (b *Backend) ensureManagedWorkspace(worker *record) error {
	workspaceID := worker.Config.Labels[managedworkspace.Label]
	if workspaceID == "" {
		return nil
	}
	b.workspaceMu.Lock()
	defer b.workspaceMu.Unlock()
	r, err := b.loadWorkspace(workspaceID)
	if err != nil {
		return err
	}
	if r.Info.State != "ready" || r.Info.Lease != worker.ID || r.Info.Kind != "workspace" || r.Info.ReadOnly != (worker.Config.Labels[managedworkspace.ReadOnlyLabel] == "true") {
		return fmt.Errorf("managed workspace lease or operation state changed")
	}
	return b.ensureWorkspaceMetadata(r)
}
func (b *Backend) clearWorkspaceMetadata(r *workspaceRecord) error {
	path := b.workspaceMetadata(r.Info.ID)
	data, err := stateMetadataFile(filepath.Join(path, "mountInfo.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	expected, _ := json.Marshal(b.workspaceMountInfo(r))
	if string(data) != string(expected) {
		return fmt.Errorf("managed workspace runtime metadata ownership mismatch")
	}
	if err = os.Remove(filepath.Join(path, "mountInfo.json")); err != nil {
		return err
	}
	if err = os.Remove(path); err != nil {
		return err
	}
	return syncStateDirectory(b.stateVolumeRoot())
}
func (b *Backend) releaseManagedWorkspace(worker *record) error {
	workspaceID := worker.Config.Labels[managedworkspace.Label]
	if workspaceID == "" {
		return nil
	}
	b.workspaceMu.Lock()
	defer b.workspaceMu.Unlock()
	r, err := b.loadWorkspace(workspaceID)
	if err != nil {
		return err
	}
	if r.Info.Lease == "" {
		return nil
	}
	if r.Info.Lease != worker.ID {
		return nil
	} // A rejected create may never have acquired this lease.
	if r.Info.State == "saving" {
		return fmt.Errorf("managed workspace lease changed; cleanup retained")
	}
	if err = b.kernelIdle(worker); err != nil {
		return err
	}
	if err = b.clearWorkspaceMetadata(r); err != nil {
		return err
	}
	r.Info.Lease = ""
	r.Info.ReadOnly = false
	return b.saveWorkspace(r)
}
func (b *Backend) removeWorkspace(r *workspaceRecord) error { return b.removeWorkspaceForPoint(r, "") }
func (b *Backend) removeWorkspaceForPoint(r *workspaceRecord, point string) error {
	if r.Info.Lease != "" || r.Info.State == "saving" {
		return fmt.Errorf("workspace is referenced by a retained VM or active operation: %w", errdefs.ErrConflict)
	}
	if err := b.workspaceReferencedByPointExcept(r.Info.ID, point); err != nil {
		return err
	}
	if err := b.validateWorkspaceRemoval(r); err != nil {
		return err
	}
	r.Info.State = "deleting"
	if err := b.saveWorkspace(r); err != nil {
		return err
	}
	if err := b.clearWorkspaceMetadata(r); err != nil {
		return err
	}
	if err := os.RemoveAll(b.workspacePath(r.Info.ID)); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(b.workspaceRecords(), r.Info.ID+".seed")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(filepath.Join(b.workspaceRecords(), r.Info.ID+".json")); err != nil {
		return err
	}
	return syncStateDirectory(b.workspaceRecords())
}

// A failed reservation may have a short disk. Cleanup still requires its exact
// recorded inode, never a guessed file at the same path. A missing disk is only
// expected after deletion began or when creation never acquired an inode.
func (b *Backend) validateWorkspaceRemoval(r *workspaceRecord) error {
	dirErr := existingPrivateDirectory(b.workspacePath(r.Info.ID))
	if dirErr != nil && (!errors.Is(dirErr, os.ErrNotExist) || r.Info.State != "deleting") {
		return dirErr
	}
	st, err := os.Lstat(b.workspaceImage(r.Info.ID))
	if errors.Is(err, os.ErrNotExist) && r.Info.State == "deleting" {
		return nil
	}
	if errors.Is(err, os.ErrNotExist) && r.Info.State == "empty" && r.Inode == 0 && r.Device == 0 {
		entries, e := os.ReadDir(b.workspacePath(r.Info.ID))
		if e != nil {
			return e
		}
		if len(entries) == 0 {
			return nil
		}
		return fmt.Errorf("unrecorded files in incomplete workspace; retained")
	}
	if err != nil {
		return err
	}
	stat, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 || uint64(stat.Dev) != r.Device || stat.Ino != r.Inode || r.Inode == 0 || st.Size() > r.Info.Options.Bytes {
		return fmt.Errorf("workspace disk ownership changed; retained")
	}
	return nil
}

func (b *Backend) workspacesHTTP(w http.ResponseWriter, request *http.Request, ctx context.Context, path string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if request.Method == http.MethodPost || request.Method == http.MethodDelete {
		b.mu.Lock()
		defer b.mu.Unlock()
	}
	b.workspaceMu.Lock()
	defer b.workspaceMu.Unlock()
	if path == "/workspaces" {
		if request.Method == http.MethodGet {
			out, err := b.workspaceCatalog()
			if err != nil {
				failure(w, err)
			} else {
				reply(w, out)
			}
			return
		}
		if request.Method == http.MethodPost {
			var options managedworkspace.Options
			if err := decode(request, &options); err != nil {
				failure(w, err)
				return
			}
			out, err := b.newWorkspace(ctx, options)
			if err != nil {
				if out != nil {
					err = fmt.Errorf("workspace %s retained for inspection: %w", out.Info.ID, err)
				}
				failure(w, err)
			} else {
				reply(w, out.Info)
			}
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/workspaces/"), "/")
	if len(parts) > 2 {
		http.NotFound(w, request)
		return
	}
	r, err := b.loadWorkspace(parts[0])
	if err != nil {
		failure(w, err)
		return
	}
	if len(parts) == 1 {
		switch request.Method {
		case http.MethodGet:
			reply(w, r.Info)
		case http.MethodDelete:
			if err = b.removeWorkspace(r); err != nil {
				failure(w, err)
			} else {
				reply(w, map[string]bool{"removed": true})
			}
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	switch parts[1] {
	case "seed":
		if request.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(5 * time.Minute))
		// Each reviewed UTF-8/long path may need a PAX header and its padded
		// payload in addition to the regular header and file padding.
		limit := r.Info.Options.SeedLimit() + int64(managedworkspace.MaxFiles)*3072 + managedworkspace.MaxManifestBytes + 2048
		err = b.seedWorkspace(ctx, r, http.MaxBytesReader(w, request.Body, limit))
		if err != nil {
			failure(w, err)
		} else {
			reply(w, r.Info)
		}
	case "manifest":
		if request.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		out, e := b.workspaceManifest(r)
		if e != nil {
			failure(w, e)
		} else {
			reply(w, out)
		}
	case "clone":
		if request.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		if err = decode(request, &body); err != nil {
			failure(w, err)
			return
		}
		out, e := b.cloneWorkspace(ctx, r, body.Name)
		if e != nil {
			failure(w, e)
		} else {
			reply(w, out.Info)
		}
	default:
		http.NotFound(w, request)
	}
}

// The exact raw disk is copied and hashed; no guest filesystem is mounted or
// interpreted. Snapshot identity is rechecked on every clone.
func (b *Backend) copyWorkspaceDisk(ctx context.Context, source, target *workspaceRecord) (string, error) {
	if err := b.validateWorkspaceDisk(source); err != nil {
		return "", err
	}
	input, err := os.OpenFile(b.workspaceImage(source.Info.ID), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", err
	}
	defer func() { _ = input.Close() }()
	output, err := os.OpenFile(b.workspaceImage(target.Info.ID), os.O_WRONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", err
	}
	defer func() { _ = output.Close() }()
	hash := sha256.New()
	n, err := io.CopyN(io.MultiWriter(output, hash), &contextReader{ctx, input}, source.Info.Options.Bytes)
	if err != nil {
		return "", err
	}
	if n != target.Info.Options.Bytes {
		return "", fmt.Errorf("workspace clone size mismatch")
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if source.Info.Kind == "snapshot" && source.Info.DiskSHA256 != digest {
		return "", fmt.Errorf("workspace snapshot checksum changed")
	}
	if err = output.Sync(); err != nil {
		return "", err
	}
	return digest, b.validateWorkspaceDisk(target)
}
func (b *Backend) cloneWorkspace(ctx context.Context, source *workspaceRecord, name string) (*workspaceRecord, error) {
	if source.Info.Kind != "snapshot" || source.Info.State != "ready" || source.Info.Lease != "" {
		return nil, fmt.Errorf("clone requires an immutable ready workspace snapshot")
	}
	options := source.Info.Options
	options.Name = name
	target, err := b.newWorkspace(ctx, options)
	if err != nil {
		return target, err
	}
	target.Info.State = "restoring"
	if err = b.saveWorkspace(target); err != nil {
		return target, err
	}
	if _, err = b.copyWorkspaceDisk(ctx, source, target); err != nil {
		return target, fmt.Errorf("incomplete workspace %s retained: %w", target.Info.ID, err)
	}
	manifest, err := b.workspaceManifest(source)
	if err != nil {
		return target, err
	}
	if err = saveWorkspaceJSON(filepath.Join(b.workspaceRecords(), target.Info.ID+".seed"), manifest); err != nil {
		return target, err
	}
	target.Info.SeedID, target.Info.SeedFiles, target.Info.SeedBytes = source.Info.SeedID, source.Info.SeedFiles, source.Info.SeedBytes
	target.Info.State = "ready"
	return target, b.saveWorkspace(target)
}
