package sandboxux

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/pacificbelt30/codex-dock/internal/sandbox"
)

const TextLimit = 256 << 10

type FileAPI interface {
	ListFileEntries(context.Context, string, string, io.Writer) error
	ReadFile(context.Context, string, string, io.Writer) error
	CreateFile(context.Context, string, string, io.Reader) error
}
type FileEntry struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Size int64  `json:"size"`
}
type Files struct{ Guest FileAPI }

func GuestPath(p string, root bool) error {
	if root && p == "." {
		return nil
	}
	if p == "" || len(p) > 4096 || path.Clean(p) != p || strings.HasPrefix(p, "/") || !utf8.ValidString(p) {
		return fmt.Errorf("choose a relative workspace path")
	}
	for _, part := range strings.Split(p, "/") {
		if part == "." || part == ".." || part == "" {
			return fmt.Errorf("dot path components are not allowed")
		}
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return fmt.Errorf("control characters are not allowed in workspace paths")
		}
	}
	return nil
}
func (f Files) List(ctx context.Context, id, dir string) ([]FileEntry, error) {
	if err := GuestPath(dir, true); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := f.Guest.ListFileEntries(ctx, id, dir, &LimitedWriter{Writer: &out, Remaining: 16 << 20}); err != nil {
		return nil, err
	}
	var entries []FileEntry
	if err := json.Unmarshal(out.Bytes(), &entries); err != nil {
		return nil, err
	}
	if len(entries) > 10000 {
		return nil, fmt.Errorf("directory exceeds 10000 entries")
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if GuestPath(e.Name, false) != nil || strings.Contains(e.Name, "/") || seen[e.Name] || e.Size < 0 {
			return nil, fmt.Errorf("guest returned invalid directory metadata")
		}
		seen[e.Name] = true
		switch e.Kind {
		case "file", "directory", "symlink", "other":
		default:
			return nil, fmt.Errorf("guest returned an invalid file type")
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if (entries[i].Kind == "directory") != (entries[j].Kind == "directory") {
			return entries[i].Kind == "directory"
		}
		return entries[i].Name < entries[j].Name
	})
	return entries, nil
}

type Text struct {
	Content           string
	Truncated, Binary bool
}

func (f Files) Text(ctx context.Context, id, name string) (Text, error) {
	if err := GuestPath(name, false); err != nil {
		return Text{}, err
	}
	var out bytes.Buffer
	writer := &LimitedWriter{Writer: &out, Remaining: TextLimit}
	err := f.Guest.ReadFile(ctx, id, name, writer)
	if err != nil && !writer.Exceeded {
		return Text{}, err
	}
	data := out.Bytes()
	if !utf8.Valid(data) && writer.Exceeded {
		for len(data) > 0 && !utf8.Valid(data) && len(out.Bytes())-len(data) < 4 {
			data = data[:len(data)-1]
		}
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return Text{Binary: true, Truncated: writer.Exceeded}, nil
	}
	var display strings.Builder
	for _, r := range strings.ReplaceAll(string(data), "\r\n", "\n") {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			if r <= 255 {
				fmt.Fprintf(&display, "\\x%02x", r)
			} else {
				fmt.Fprintf(&display, "\\u%04x", r)
			}
		} else {
			display.WriteRune(r)
		}
	}
	return Text{Content: display.String(), Truncated: writer.Exceeded}, nil
}
func (f Files) Download(ctx context.Context, id, guest, local string) error {
	if err := GuestPath(guest, false); err != nil {
		return err
	}
	return createHostFile(local, func(w io.Writer) error {
		return f.Guest.ReadFile(ctx, id, guest, &LimitedWriter{Writer: w, Remaining: sandbox.MaxFileBytes})
	})
}

type Upload struct {
	file   *os.File
	Size   int64
	SHA256 string
}

func (u *Upload) Close() error { return u.file.Close() }
func StageUpload(local string) (*Upload, error) {
	source, err := openHostSource(local)
	if err != nil {
		return nil, err
	}
	defer func() { _ = source.Close() }()
	st, err := source.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() > sandbox.MaxFileBytes {
		return nil, fmt.Errorf("upload exceeds 64 MiB")
	}
	copy, err := os.CreateTemp("", "oaiso-upload-")
	if err != nil {
		return nil, err
	}
	if err = os.Remove(copy.Name()); err != nil {
		_ = copy.Close()
		return nil, err
	}
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(copy, hash), io.LimitReader(source, sandbox.MaxFileBytes+1))
	if err != nil || size > sandbox.MaxFileBytes {
		_ = copy.Close()
		return nil, fmt.Errorf("upload snapshot failed or exceeded 64 MiB")
	}
	if _, err = copy.Seek(0, io.SeekStart); err != nil {
		_ = copy.Close()
		return nil, err
	}
	return &Upload{file: copy, Size: size, SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}
func (f Files) Upload(ctx context.Context, id, guest string, u *Upload) error {
	if err := GuestPath(guest, false); err != nil {
		return err
	}
	if _, err := u.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return f.Guest.CreateFile(ctx, id, guest, io.LimitReader(u.file, sandbox.MaxFileBytes+1))
}
