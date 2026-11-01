//go:build linux

package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/pacificbelt30/codex-dock/internal/proxyimage"
)

func (b *Backend) authorityRoot() string { return filepath.Join(b.opts.State, "authority") }

func (b *Backend) ensureAuthorityRoot() error {
	path := b.authorityRoot()
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || s.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("node authority root must be private and node-owned")
	}
	return nil
}

func (b *Backend) authorityHTTP(w http.ResponseWriter, r *http.Request, ctx context.Context, path string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.ensureAuthorityRoot(); err != nil {
		failure(w, err)
		return
	}
	if path == "/authority/images" {
		dir := filepath.Join(b.authorityRoot(), "images")
		if r.Method == http.MethodGet {
			a, err := proxyimage.Load(dir, r.URL.Query().Get("name"))
			if os.IsNotExist(err) {
				http.NotFound(w, r)
				return
			}
			if err != nil {
				failure(w, err)
				return
			}
			reply(w, a)
			return
		}
		if r.Method == http.MethodPost {
			var a proxyimage.Authority
			if err := decode(r, &a); err != nil {
				failure(w, err)
				return
			}
			img, _, err := b.image(ctx, a.Name)
			if err == nil && img.Target().Digest.String() != a.Digest {
				err = fmt.Errorf("proxy image changed before authorization")
			}
			if err == nil {
				err = b.protectAuthorityShares(ctx)
			}
			if err == nil {
				err = proxyimage.Save(dir, a)
			}
			if err != nil {
				failure(w, err)
				return
			}
			a.Name, _ = proxyimage.Normalize(a.Name)
			reply(w, a)
			return
		}
	}
	if path == "/authority/cases" {
		if r.Method == http.MethodPost {
			var req struct {
				Name, CaseID, Secret string
				UID, GID             int
			}
			if err := decode(r, &req); err != nil {
				failure(w, err)
				return
			}
			if !validName.MatchString(req.Name) || req.CaseID == "" || len(req.CaseID) > 256 || strings.ContainsAny(req.CaseID, "\x00\r\n") || req.UID < 0 || req.GID < 0 || len(req.Secret) < 32 || len(req.Secret) > 4096 || strings.TrimSpace(req.Secret) != req.Secret || strings.ContainsAny(req.Secret, "\x00\r\n") {
				failure(w, fmt.Errorf("invalid case authority"))
				return
			}
			// Admission precedes publication, and every future managed worker
			// share is checked against the entire authority root, not one file.
			if err := b.protectAuthorityShares(ctx); err != nil {
				failure(w, err)
				return
			}
			file := b.caseAuthorityPath(req.Name, req.CaseID)
			if err := writeCaseAuthority(file, req.Secret, req.UID, req.GID); err != nil {
				failure(w, err)
				return
			}
			reply(w, map[string]string{"Path": file})
			return
		}
		if r.Method == http.MethodDelete {
			file := b.caseAuthorityPath(r.URL.Query().Get("name"), r.URL.Query().Get("case"))
			all, err := b.client.Containers(ctx, `labels."`+owned+`"=="true"`)
			if err != nil {
				failure(w, err)
				return
			}
			for _, c := range all {
				rec, _, err := b.loadOwned(ctx, c)
				if err != nil {
					failure(w, err)
					return
				}
				for _, m := range rec.Host.Mounts {
					if m.Source == file {
						failure(w, fmt.Errorf("case authority is still referenced by a retained proxy"))
						return
					}
				}
			}
			if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
				failure(w, err)
				return
			}
			if err := syncStateDirectory(b.authorityRoot()); err != nil {
				failure(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	http.NotFound(w, r)
}

func (b *Backend) caseAuthorityPath(name, caseID string) string {
	sum := sha256.Sum256([]byte(name + "\x00" + caseID))
	return filepath.Join(b.authorityRoot(), "case-"+hex.EncodeToString(sum[:]))
}

func writeCaseAuthority(path, secret string, uid, gid int) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0600)
	if os.IsExist(err) {
		f, err = os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		s, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || s.Uid != uint32(uid) || s.Gid != uint32(gid) || s.Nlink != 1 || info.Size() > 4096 {
			return fmt.Errorf("case authority identity differs")
		}
		data, err := io.ReadAll(io.LimitReader(f, 4097))
		if err != nil {
			return err
		}
		if string(data) != secret {
			return fmt.Errorf("case authority differs; drain and remove old proxy before replacement")
		}
		return nil
	}
	if err != nil {
		return err
	}
	success := false
	defer func() {
		_ = f.Close()
		if !success {
			_ = os.Remove(path)
		}
	}()
	if err := f.Chown(uid, gid); err != nil {
		return err
	}
	if _, err := io.WriteString(f, secret); err != nil {
		return err
	}
	if err := errors.Join(f.Sync(), f.Close()); err != nil {
		return err
	}
	if err := syncStateDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	success = true
	return nil
}
