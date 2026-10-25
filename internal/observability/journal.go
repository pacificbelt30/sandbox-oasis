package observability

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

const DefaultMaxBytes int64 = 1 << 20

// Journal keeps current+previous files, each bounded by MaxBytes. This is local
// diagnostic history, not a tamper-resistant audit log or delivery guarantee.
type Journal struct {
	Directory string
	MaxBytes  int64
}

func (j Journal) lock() (func(), int64, error) {
	limit := j.MaxBytes
	if limit == 0 {
		limit = DefaultMaxBytes
	}
	if limit < 1024 || limit > 8<<20 || !filepath.IsAbs(j.Directory) || filepath.Clean(j.Directory) != j.Directory {
		return nil, 0, fmt.Errorf("invalid diagnostic directory or size (1 KiB–8 MiB)")
	}
	if err := os.MkdirAll(j.Directory, 0700); err != nil {
		return nil, 0, fmt.Errorf("diagnostic directory unavailable")
	}
	info, err := os.Lstat(j.Directory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 || !privateOwner(info) {
		return nil, 0, fmt.Errorf("diagnostic directory must be owner-private (0700)")
	}
	f, err := os.OpenFile(filepath.Join(j.Directory, ".lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, 0, fmt.Errorf("diagnostic lock unavailable")
	}
	info, err = f.Stat()
	if err != nil || !privateRegular(info) {
		_ = f.Close()
		return nil, 0, fmt.Errorf("unsafe diagnostic lock")
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, 0, fmt.Errorf("diagnostic lock unavailable")
	}
	return func() { _ = f.Close() }, limit, nil
}

func privateOwner(info os.FileInfo) bool {
	s, ok := info.Sys().(*syscall.Stat_t)
	return ok && s.Uid == uint32(os.Geteuid())
}
func privateRegular(info os.FileInfo) bool {
	s, ok := info.Sys().(*syscall.Stat_t)
	return ok && s.Nlink == 1 && privateOwner(info) && info.Mode().IsRegular() && info.Mode().Perm()&0077 == 0
}

func checkFile(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !privateRegular(info) {
		return fmt.Errorf("unsafe diagnostic history file")
	}
	return nil
}

func (j Journal) Append(record Record) error {
	if err := record.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil || len(data) > 16<<10 {
		return fmt.Errorf("diagnostic record exceeds limit")
	}
	data = append(data, '\n')
	unlock, limit, err := j.lock()
	if err != nil {
		return err
	}
	defer unlock()
	current, previous := filepath.Join(j.Directory, "events.jsonl"), filepath.Join(j.Directory, "events.previous.jsonl")
	for _, path := range []string{current, previous} {
		if err := checkFile(path); err != nil {
			return err
		}
	}
	if int64(len(data)) > limit {
		return fmt.Errorf("diagnostic record exceeds file budget")
	}
	if info, err := os.Stat(current); err == nil && info.Size()+int64(len(data)) > limit {
		if err := os.Rename(current, previous); err != nil {
			return fmt.Errorf("rotating diagnostics failed")
		}
	}
	f, err := os.OpenFile(current, os.O_CREATE|os.O_WRONLY|os.O_APPEND|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return fmt.Errorf("diagnostic history unavailable")
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !privateRegular(info) {
		return fmt.Errorf("unsafe diagnostic history")
	}
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("saving diagnostics failed")
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("syncing diagnostics failed")
	}
	return nil
}

func (j Journal) Read() ([]Record, error) {
	unlock, limit, err := j.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	var result []Record
	for _, name := range []string{"events.previous.jsonl", "events.jsonl"} {
		path := filepath.Join(j.Directory, name)
		if err := checkFile(path); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("diagnostic history unavailable")
		}
		info, err := f.Stat()
		if err != nil || !privateRegular(info) || info.Size() > limit {
			_ = f.Close()
			return nil, fmt.Errorf("invalid diagnostic history")
		}
		data, err := io.ReadAll(io.LimitReader(f, limit+1))
		_ = f.Close()
		if err != nil || int64(len(data)) > limit {
			return nil, fmt.Errorf("diagnostic history exceeds budget")
		}
		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 4096), 16<<10)
		for scanner.Scan() {
			var r Record
			decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&r); err != nil || r.Validate() != nil {
				return nil, fmt.Errorf("invalid diagnostic record")
			}
			var extra any
			if err := decoder.Decode(&extra); err != io.EOF {
				return nil, fmt.Errorf("invalid diagnostic record")
			}
			result = append(result, r)
		}
		if scanner.Err() != nil {
			return nil, fmt.Errorf("oversized diagnostic record")
		}
	}
	return result, nil
}
