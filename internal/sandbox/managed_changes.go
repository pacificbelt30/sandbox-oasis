package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/managedworkspace"
)

const managedChangesProgram = `
import os,sys,json,stat,hashlib
root=os.open('/workspace',os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
files=[];total=0;count=0
def excluded(n):
 n=n.lower()
 return n in ('.git','node_modules','.venv','venv','__pycache__','.cache','.ssh','.aws','.azure','.codex','.claude','.config','auth.json','credentials.json','.npmrc','.netrc','.pypirc','lost+found') or n.startswith('.env') or n.endswith(('.pem','.key','.p12','.pfx'))
def walk(fd,prefix,depth):
 global total,count
 if depth>64:raise ValueError('directory depth limit')
 with os.scandir(fd) as entries:
  for entry in entries:
   count+=1
   if count>100000:raise ValueError('scan limit')
   if excluded(entry.name) or entry.is_symlink():continue
   rel=prefix+entry.name
   if entry.is_dir(follow_symlinks=False):
    child=os.open(entry.name,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW,dir_fd=fd)
    try:walk(child,rel+'/',depth+1)
    finally:os.close(child)
   else:
    child=os.open(entry.name,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK,dir_fd=fd)
    with os.fdopen(child,'rb') as f:
     s=os.fstat(f.fileno())
     if not stat.S_ISREG(s.st_mode):continue
     if s.st_size>67108864:raise ValueError('file exceeds diff limit; use F download')
     h=hashlib.sha256();size=0
     while True:
      data=f.read(65536)
      if not data:break
      size+=len(data);total+=len(data)
      if size>67108864 or total>536870912:raise ValueError('diff byte limit')
      h.update(data)
     files.append(dict(path=rel,bytes=size,mode=493 if s.st_mode&0o111 else 420,sha256=h.hexdigest()))
     if len(files)>8192:raise ValueError('diff file limit')
walk(root,'',0)
print(json.dumps(files,ensure_ascii=True))
`

// Managed changes compare guest-observed hashes with the private initial import
// manifest, including for restored copies. No guest .git is read on the host.
func (m *Manager) ManagedWorkspaceChanges(ctx context.Context, id string) (string, error) {
	info, err := m.ManagedWorkspaceInfo(ctx, id)
	if err != nil || info == nil {
		return "", err
	}
	baseline, err := engine.ManagedWorkspaceManifest(ctx, m.cli, info.ID)
	if err != nil {
		return "", err
	}
	var out, stderr bytes.Buffer
	code, err := m.Exec(ctx, id, []string{"python3", "-I", "-c", managedChangesProgram}, nil, &fileOutput{out: &out, remaining: 2 << 20}, &fileOutput{out: &stderr, remaining: 64 << 10})
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("managed workspace comparison failed (exit %d); use F for individual file downloads", code)
	}
	var files []managedworkspace.File
	if err = json.Unmarshal(out.Bytes(), &files); err != nil {
		return "", err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	current := managedworkspace.Manifest{Files: files}
	for _, f := range files {
		current.Bytes += f.Bytes
	}
	current.ID = current.Digest()
	if err = current.Validate(managedworkspace.MaxSeedBytes); err != nil {
		return "", err
	}
	old := map[string]managedworkspace.File{}
	for _, f := range baseline.Files {
		old[f.Path] = f
	}
	var lines []string
	for _, f := range current.Files {
		previous, ok := old[f.Path]
		if !ok {
			lines = append(lines, "A "+f.Path)
		} else if previous != f {
			lines = append(lines, "M "+f.Path)
		}
		delete(old, f.Path)
	}
	for p := range old {
		lines = append(lines, "D "+p)
	}
	sort.Strings(lines)
	if len(lines) == 0 {
		lines = append(lines, "No changes among compared regular files.")
	}
	return "Guest-observed file hashes against initial import " + baseline.ID + "\nIncludes additions/modifications/deletions; secrets/cache paths and links are excluded. This is a file summary, not a Git patch. Restored copies still compare against the initial import.\n\n" + strings.Join(lines, "\n"), nil
}
