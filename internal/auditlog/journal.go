// Package auditlog stores bounded, secret-free local operation evidence.
// Hashes detect accidental edits and gaps; the local owner/root remains trusted.
package auditlog

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

const DefaultMaxBytes int64 = 16 << 20

type Journal struct {
	Directory string
	MaxBytes  int64
}
type Record struct {
	Schema      int       `json:"schema"`
	Sequence    uint64    `json:"sequence"`
	Time        time.Time `json:"time"`
	Transaction string    `json:"transaction"`
	Action      string    `json:"action"`
	Subject     string    `json:"subject_hash"`
	Result      string    `json:"result"`
	Previous    string    `json:"previous"`
	Hash        string    `json:"hash"`
}
type Report struct {
	Records []Record `json:"records"`
	Pending []string `json:"pending"`
}

var actions = map[string]bool{"create": true, "start": true, "stop": true, "delete": true, "checkpoint": true, "auth_change": true, "permission_change": true, "issue": true, "restore": true, "revoke": true, "denial": true}

func Digest(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func hash(r Record) string   { r.Hash = ""; b, _ := json.Marshal(r); return Digest(string(b)) }
func private(f *os.File, dir bool) error {
	i, e := f.Stat()
	if e != nil {
		return e
	}
	var s unix.Stat_t
	if e = unix.Fstat(int(f.Fd()), &s); e != nil {
		return e
	}
	if s.Uid != uint32(os.Geteuid()) || i.Mode().Perm()&0077 != 0 || (dir && !i.IsDir()) || (!dir && (!i.Mode().IsRegular() || s.Nlink != 1)) {
		return errors.New("unsafe audit storage")
	}
	return nil
}
func openat(d *os.File, name string, flags int) (*os.File, error) {
	fd, e := unix.Openat(int(d.Fd()), name, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0600)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), name)
	if e = private(f, false); e != nil {
		_ = f.Close()
		return nil, e
	}
	return f, nil
}
func (j Journal) locked(fn func(*os.File, int64) error) error {
	if !filepath.IsAbs(j.Directory) || filepath.Clean(j.Directory) != j.Directory {
		return errors.New("absolute audit directory required")
	}
	limit := j.MaxBytes
	if limit == 0 {
		limit = DefaultMaxBytes
	}
	if limit < 1024 || limit > 64<<20 {
		return errors.New("invalid audit capacity")
	}
	if e := os.MkdirAll(j.Directory, 0700); e != nil {
		return errors.New("audit directory unavailable")
	}
	fd, e := unix.Open(j.Directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return errors.New("audit directory unavailable")
	}
	d := os.NewFile(uintptr(fd), j.Directory)
	defer func() { _ = d.Close() }()
	if e = private(d, true); e != nil {
		return e
	}
	l, e := openat(d, ".lock", unix.O_CREAT|unix.O_RDWR)
	if e != nil {
		return errors.New("audit lock unavailable")
	}
	defer func() { _ = l.Close() }()
	if e = unix.Flock(int(l.Fd()), unix.LOCK_EX); e != nil {
		return e
	}
	return fn(d, limit)
}
func read(d *os.File, name string, limit int64) ([]byte, error) {
	f, e := openat(d, name, unix.O_RDONLY)
	if errors.Is(e, unix.ENOENT) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	defer func() { _ = f.Close() }()
	i, e := f.Stat()
	if e != nil || i.Size() > limit {
		return nil, errors.New("audit file exceeds capacity")
	}
	return io.ReadAll(io.LimitReader(f, limit+1))
}
func verify(data, head []byte) (Report, error) {
	out := Report{Records: []Record{}, Pending: []string{}}
	previous := ""
	seq := uint64(0)
	pending := map[string]bool{}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		return out, errors.New("partial audit record")
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 1024), 4096)
	for scanner.Scan() {
		var r Record
		dec := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		dec.DisallowUnknownFields()
		if e := dec.Decode(&r); e != nil {
			return out, errors.New("invalid audit record")
		}
		if r.Schema != 1 || r.Sequence != seq+1 || r.Previous != previous || r.Hash != hash(r) || !actions[r.Action] || len(r.Subject) != 64 || len(r.Transaction) != 32 || r.Time.IsZero() {
			return out, errors.New("audit chain mismatch")
		}
		switch r.Result {
		case "intent":
			if pending[r.Transaction] {
				return out, errors.New("duplicate audit intent")
			}
			pending[r.Transaction] = true
		case "ok", "error", "unknown":
			if !pending[r.Transaction] {
				return out, errors.New("audit result missing intent")
			}
			delete(pending, r.Transaction)
		default:
			return out, errors.New("invalid audit result")
		}
		previous = r.Hash
		seq = r.Sequence
		out.Records = append(out.Records, r)
	}
	if scanner.Err() != nil {
		return out, errors.New("oversized audit record")
	}
	if string(head) != previous {
		return out, errors.New("audit head mismatch: missing or uncommitted records")
	}
	for tx := range pending {
		out.Pending = append(out.Pending, tx)
	}
	return out, nil
}
func (j Journal) Read() (out Report, err error) {
	err = j.locked(func(d *os.File, limit int64) error {
		data, e := read(d, "events.jsonl", limit)
		if e != nil {
			return e
		}
		head, e := read(d, "head", 64)
		if e != nil {
			return e
		}
		out, e = verify(data, head)
		return e
	})
	return
}
func (j Journal) append(action, subject, tx, result string) error {
	return j.locked(func(d *os.File, limit int64) error {
		data, e := read(d, "events.jsonl", limit)
		if e != nil {
			return e
		}
		head, e := read(d, "head", 64)
		if e != nil {
			return e
		}
		report, e := verify(data, head)
		if e != nil {
			return e
		}
		r := Record{Schema: 1, Sequence: uint64(len(report.Records)) + 1, Time: time.Now().UTC(), Transaction: tx, Action: action, Subject: Digest(subject), Result: result, Previous: string(head)}
		r.Hash = hash(r)
		line, _ := json.Marshal(r)
		line = append(line, '\n')
		if int64(len(data)+len(line)) > limit {
			return errors.New("audit capacity exhausted; archive while service is stopped")
		}
		f, e := openat(d, "events.jsonl", unix.O_CREAT|unix.O_WRONLY|unix.O_APPEND)
		if e != nil {
			return errors.New("audit write unavailable")
		}
		n, we := f.Write(line)
		if we == nil && n != len(line) {
			we = io.ErrShortWrite
		}
		if we == nil {
			we = f.Sync()
		}
		ce := f.Close()
		if we != nil || ce != nil {
			return errors.New("audit write failed; outcome unknown")
		}
		// A mismatched head after a crash is deliberately an error, never silently repaired.
		temp := ".head-" + tx
		h, e := openat(d, temp, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY)
		if e != nil {
			return errors.New("audit head unavailable")
		}
		defer func() { _ = unix.Unlinkat(int(d.Fd()), temp, 0) }()
		_, we = h.WriteString(r.Hash)
		if we == nil {
			we = h.Sync()
		}
		ce = h.Close()
		if we != nil || ce != nil {
			return errors.New("audit head write failed")
		}
		if e = unix.Renameat(int(d.Fd()), temp, int(d.Fd()), "head"); e != nil {
			return e
		}
		return d.Sync()
	})
}

// Begin must succeed before granting authority. Finish failure means the
// operation may have completed: callers must reconcile rather than retry blindly.
func (j Journal) Begin(action, subject string) (func(error) error, error) {
	if !actions[action] {
		return nil, fmt.Errorf("unknown audit action")
	}
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return nil, e
	}
	tx := hex.EncodeToString(b[:])
	if e := j.append(action, subject, tx, "intent"); e != nil {
		return nil, e
	}
	return func(result error) error {
		status := "ok"
		if result != nil {
			status = "error"
		}
		return j.append(action, subject, tx, status)
	}, nil
}
