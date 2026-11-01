// Package agentbackup describes opaque, offline private agent disk backups.
// Filesystem contents are only interpreted inside a new Kata VM.
package agentbackup

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
)

const Magic = "OAISODB1"
const MaxBytes int64 = 8 << 30
const MaxFileBytes int64 = MaxBytes + 4108

var hashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Header struct {
	Format string `json:"format"`
	Bytes  int64  `json:"bytes"`
	UID    uint32 `json:"uid"`
	GID    uint32 `json:"gid"`
	SHA256 string `json:"sha256"`
}

func (h Header) Validate() error {
	if h.Format != "oaiso-agent-state-v1" || h.Bytes < 64<<20 || h.Bytes > MaxBytes || !hashPattern.MatchString(h.SHA256) {
		return fmt.Errorf("invalid agent state backup header")
	}
	return nil
}
func WriteHeader(w io.Writer, h Header) error {
	if err := h.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(h)
	if err != nil {
		return err
	}
	prefix := make([]byte, 12)
	copy(prefix, Magic)
	binary.BigEndian.PutUint32(prefix[8:], uint32(len(data)))
	if _, err = w.Write(prefix); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
func ReadHeader(r io.Reader) (Header, error) {
	var h Header
	var prefix [12]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return h, err
	}
	size := binary.BigEndian.Uint32(prefix[8:])
	if string(prefix[:8]) != Magic || size == 0 || size > 4096 {
		return h, fmt.Errorf("invalid agent state backup prefix")
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(r, data); err != nil {
		return h, err
	}
	if err := json.Unmarshal(data, &h); err != nil {
		return h, err
	}
	return h, h.Validate()
}

// CopyPayload requires exact length and digest, including EOF. It can never
// report success for a truncated stream or appended second backup.
func CopyPayload(w io.Writer, r io.Reader, h Header) error {
	hash := sha256.New()
	n, err := io.CopyN(io.MultiWriter(w, hash), r, h.Bytes)
	if err != nil {
		return fmt.Errorf("incomplete agent state payload (%d bytes): %w", n, err)
	}
	var extra [1]byte
	nread, err := io.ReadFull(r, extra[:])
	if nread != 0 || err != io.EOF {
		return fmt.Errorf("agent state backup has trailing data or missing EOF")
	}
	if hex.EncodeToString(hash.Sum(nil)) != h.SHA256 {
		return fmt.Errorf("agent state backup checksum mismatch")
	}
	return nil
}
func ValidateFile(f *os.File) (Header, error) {
	var empty Header
	st, err := f.Stat()
	if err != nil {
		return empty, err
	}
	if !st.Mode().IsRegular() || st.Size() > MaxFileBytes {
		return empty, fmt.Errorf("agent state backup must be a bounded regular file")
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return empty, err
	}
	h, err := ReadHeader(f)
	if err != nil {
		return h, err
	}
	return h, CopyPayload(io.Discard, f, h)
}
