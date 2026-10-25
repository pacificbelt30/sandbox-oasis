package usage

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

type document struct {
	Schema   int                `json:"schema"`
	Accounts map[string]account `json:"accounts"`
}

// Open holds an exclusive private file lock for the proxy lifetime. Only
// counters, hashed execution IDs and policies are stored, never credentials.
func Open(dir string) (*Ledger, error) {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return nil, fmt.Errorf("usage state requires a clean absolute directory")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, ErrUnavailable
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 || !owned(info) {
		return nil, fmt.Errorf("usage directory must be owner-private (0700)")
	}
	lock, err := os.OpenFile(filepath.Join(dir, "ledger.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, ErrUnavailable
	}
	good := false
	defer func() {
		if !good {
			_ = lock.Close()
		}
	}()
	info, err = lock.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !owned(info) {
		return nil, ErrUnavailable
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, fmt.Errorf("usage ledger is already in use")
	}
	path := filepath.Join(dir, "ledger.json")
	l := &Ledger{accounts: make(map[string]account), release: lock.Close}
	l.persist = func(accounts map[string]account) error { return writeDocument(dir, path, accounts) }
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if os.IsNotExist(err) {
		if err := l.persist(l.accounts); err != nil {
			return nil, ErrUnavailable
		}
	} else if err != nil {
		return nil, ErrUnavailable
	} else {
		defer func() { _ = file.Close() }()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !owned(info) || info.Size() > 8<<20 {
			return nil, ErrUnavailable
		}
		var doc document
		decoder := json.NewDecoder(io.LimitReader(file, 8<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&doc); err != nil || doc.Schema != 1 || doc.Accounts == nil || len(doc.Accounts) > 10000 {
			return nil, ErrUnavailable
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return nil, ErrUnavailable
		}
		for key, a := range doc.Accounts {
			decoded, err := hex.DecodeString(key)
			if err != nil || len(decoded) != 32 || a.Policy.Validate() != nil || a.Requests < 0 || a.Active < 0 || a.Pending < 0 || a.Active > a.Pending || a.Pending > a.Requests || a.Bytes < 0 || a.Tokens < 0 || a.UnmeteredRequests < 0 || a.UnmeteredRequests > a.Requests {
				return nil, ErrUnavailable
			}
			if a.Pending > 0 {
				a.Indeterminate = true
				a.Active = 0
			}
			l.accounts[key] = a
		}
	}
	good = true
	return l, nil
}

func owned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}

func writeDocument(dir, path string, accounts map[string]account) error {
	data, err := json.Marshal(document{Schema: 1, Accounts: accounts})
	if err != nil || len(data) > 8<<20 {
		return ErrUnavailable
	}
	file, err := os.CreateTemp(dir, ".usage-")
	if err != nil {
		return err
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return directory.Sync()
}
