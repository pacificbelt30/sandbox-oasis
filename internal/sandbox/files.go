package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
)

const MaxFileBytes int64 = 64 << 20

// File operations stay in the guest and therefore retain its mount policies.
// Every path component is opened relative to /workspace with O_NOFOLLOW.
const fileProgram = `
import os,sys,json,tempfile
op,rel=sys.argv[1:3]
parts=rel.split('/')
if rel.startswith('/') or (any(p in ('','.', '..') for p in parts) and not (op in ('list','entries') and rel=='.')):
    raise SystemExit('path must be relative to /workspace without dot components')
fd=os.open('/workspace',os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
for part in parts[:-1]:
    child=os.open(part,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW,dir_fd=fd)
    os.close(fd);fd=child
name=parts[-1]
if op=='read':
    f=os.open(name,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK,dir_fd=fd)
    import stat
    if not stat.S_ISREG(os.fstat(f).st_mode):raise SystemExit('regular files only')
    if os.fstat(f).st_size>67108864:raise SystemExit('file exceeds 64 MiB')
    with os.fdopen(f,'rb') as source:
        total=0
        while True:
            data=source.read(65536)
            if not data:break
            total+=len(data)
            if total>67108864:raise ValueError('file exceeds 64 MiB')
            sys.stdout.buffer.write(data)
elif op in ('write','create'):
    import secrets
    temp='.cxd-upload-'+secrets.token_hex(16)
    f=os.open(temp,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600,dir_fd=fd)
    try:
        total=0
        with os.fdopen(f,'wb') as target:
            while True:
                data=sys.stdin.buffer.read(65536)
                if not data:break
                total+=len(data)
                if total>67108864:raise ValueError('file exceeds 64 MiB')
                target.write(data)
            target.flush();os.fsync(target.fileno())
        # Refuse replacing symlinks and non-regular destinations.
        import stat
        try:
            mode=os.stat(name,dir_fd=fd,follow_symlinks=False).st_mode
            if not stat.S_ISREG(mode):raise ValueError('regular destinations only')
        except FileNotFoundError:pass
        if op=='create':
            os.link(temp,name,src_dir_fd=fd,dst_dir_fd=fd,follow_symlinks=False)
        else:
            os.rename(temp,name,src_dir_fd=fd,dst_dir_fd=fd)
        os.fsync(fd)
    finally:
        try:os.unlink(temp,dir_fd=fd)
        except FileNotFoundError:pass
elif op in ('list','entries'):
    d=os.open(name,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW,dir_fd=fd)
    names=[]
    with os.scandir(d) as entries:
        for entry in entries:
            if op=='entries':
                import stat
                s=entry.stat(follow_symlinks=False)
                kind='directory' if stat.S_ISDIR(s.st_mode) else 'file' if stat.S_ISREG(s.st_mode) else 'symlink' if stat.S_ISLNK(s.st_mode) else 'other'
                names.append(dict(name=entry.name,kind=kind,size=s.st_size))
            else:names.append(entry.name)
            if len(names)>10000:raise SystemExit('directory exceeds 10000 entries')
    print(json.dumps(sorted(names,key=lambda x:x['name'] if isinstance(x,dict) else x)))
else:raise SystemExit('unknown operation')
`

func (m *Manager) fileOperation(ctx context.Context, id, op, path string, in io.Reader, out io.Writer) error {
	if path == "" || strings.HasPrefix(path, "/") {
		return fmt.Errorf("file path must be relative to /workspace")
	}
	var stderr bytes.Buffer
	limit := MaxFileBytes
	if op == "list" || op == "entries" {
		// 10,000 names, including JSON escaping, fit within this bound.
		limit = 16 << 20
	}
	if op == "write" || op == "create" {
		limit = 0
		if in != nil {
			// One extra byte makes the guest's atomic upload reject overflow.
			in = io.LimitReader(in, MaxFileBytes+1)
		}
	}
	if out == nil {
		out = io.Discard
	}
	code, err := m.Exec(ctx, id, []string{"python3", "-I", "-c", fileProgram, op, path}, in,
		&fileOutput{out: out, remaining: limit}, &fileOutput{out: &stderr, remaining: 64 << 10})
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("guest file %s failed (exit %d): %s", op, code, stderr.String())
	}
	return nil
}

// Guest code is untrusted even when the supplied helper bounds its own output.
// Exec cancels the guest execution when this writer returns an error.
type fileOutput struct {
	out       io.Writer
	remaining int64
}

func (w *fileOutput) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, fmt.Errorf("guest file output exceeds host limit")
	}
	n, err := w.out.Write(p)
	w.remaining -= int64(n)
	return n, err
}
func (m *Manager) ReadFile(ctx context.Context, id, path string, out io.Writer) error {
	return m.fileOperation(ctx, id, "read", path, nil, out)
}
func (m *Manager) WriteFile(ctx context.Context, id, path string, in io.Reader) error {
	return m.fileOperation(ctx, id, "write", path, in, nil)
}
func (m *Manager) ListFiles(ctx context.Context, id, path string, out io.Writer) error {
	return m.fileOperation(ctx, id, "list", path, nil, out)
}

// ExportDiff includes tracked staged and unstaged changes relative to HEAD.
// Untracked files are retrieved explicitly with ReadFile.
func (m *Manager) ExportDiff(ctx context.Context, id string, out io.Writer) error {
	code, err := m.Exec(ctx, id, []string{"git", "--no-pager", "diff", "--binary", "HEAD", "--"}, nil, out, io.Discard)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("git diff exited %d", code)
	}
	return nil
}
func (m *Manager) Commit(ctx context.Context, id, message string) error {
	if strings.TrimSpace(message) == "" {
		return fmt.Errorf("commit message is required")
	}
	code, err := m.Exec(ctx, id, []string{"git", "commit", "-m", message, "--"}, nil, io.Discard, io.Discard)
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("git commit exited %d (stage desired files and configure guest Git identity first)", code)
	}
	return nil
}

// CreateFile atomically publishes a new guest file without replacing any entry.
func (m *Manager) CreateFile(ctx context.Context, id, path string, in io.Reader) error {
	return m.fileOperation(ctx, id, "create", path, in, nil)
}

// ListFileEntries reports guest metadata without following symbolic links.
func (m *Manager) ListFileEntries(ctx context.Context, id, path string, out io.Writer) error {
	return m.fileOperation(ctx, id, "entries", path, nil, out)
}
