package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"
)

type ArtifactOptions struct {
	Paths        []string `json:"paths"`
	JobIDs       []string `json:"job_ids,omitempty"`
	MaxBytes     int64    `json:"max_bytes,omitempty"`
	ExpectedPlan string   `json:"expected_plan,omitempty"`
}
type ArtifactFile struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}
type ArtifactPlan struct {
	Jobs       []Job          `json:"jobs"`
	ImageID    string         `json:"image_id"`
	Runtime    string         `json:"runtime"`
	ID         string         `json:"id"`
	Worker     string         `json:"worker"`
	Head       string         `json:"head"`
	Files      []ArtifactFile `json:"files"`
	PatchBytes int64          `json:"patch_bytes"`
	TotalBytes int64          `json:"total_bytes"`
}

// Artifacts are explicit regular files only. No recursive path or environment
// export exists. Known credential paths are refused, not silently bundled.
const artifactProgram = `
import os,sys,json,stat,hashlib,subprocess,tempfile,tarfile,io
op,worker,raw=sys.argv[1:4]
o=json.loads(raw);paths=o['paths'];limit=o.get('max_bytes') or 33554432
if not isinstance(paths,list) or not 1<=len(paths)<=256 or not 1<=limit<=33554432:raise ValueError('invalid artifact limits')
if len(set(paths))!=len(paths):raise ValueError('duplicate artifact path')
files=[];payload=[];total=0
for rel in sorted(paths):
    if not isinstance(rel,str) or len(rel.encode())>94 or any(ord(c)<32 for c in rel):raise ValueError('artifact path is too long or contains control characters')
    parts=rel.split('/')
    if rel.startswith('/') or any(p in ('','.','..') for p in parts):raise ValueError('relative regular file paths required')
    for p in parts:
        n=p.lower()
        if n in ('.git','.ssh','.aws','.azure','.config','.codex','.claude','auth.json','credentials.json','.npmrc','.netrc','.pypirc') or n.startswith('.env') or n.endswith(('.pem','.key','.p12','.pfx')):raise ValueError('credential path excluded')
    fd=os.open('/workspace',os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW)
    try:
        for part in parts[:-1]:
            child=os.open(part,os.O_RDONLY|os.O_DIRECTORY|os.O_NOFOLLOW,dir_fd=fd);os.close(fd);fd=child
        f=os.open(parts[-1],os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK,dir_fd=fd)
        with os.fdopen(f,'rb') as source:
            if not stat.S_ISREG(os.fstat(source.fileno()).st_mode):raise ValueError('regular files required')
            data=source.read(limit-total+1)
    finally:os.close(fd)
    total+=len(data)
    if total>limit:raise ValueError('artifact size limit')
    files.append({'path':rel,'bytes':len(data),'sha256':hashlib.sha256(data).hexdigest()});payload.append(data)
# Git output is spooled to a bounded temporary file, never an unbounded pipe.
with tempfile.TemporaryFile() as patch:
    proc=subprocess.Popen(['git','--literal-pathspecs','--no-pager','diff','--binary','--no-ext-diff','--no-textconv','HEAD','--']+sorted(paths),cwd='/workspace',stdout=patch,stderr=subprocess.DEVNULL)
    import time
    while proc.poll() is None:
        if os.fstat(patch.fileno()).st_size>limit-total:proc.kill();proc.wait();raise ValueError('patch size limit')
        time.sleep(.01)
    if proc.returncode:raise ValueError('git diff failed')
    patch.seek(0);diff=patch.read(limit-total+1)
if len(diff)>limit-total:raise ValueError('patch size limit')
head=subprocess.run(['git','rev-parse','--verify','HEAD'],cwd='/workspace',stdout=subprocess.PIPE,stderr=subprocess.DEVNULL,check=True).stdout.decode().strip()
if len(head) not in (40,64) or any(c not in '0123456789abcdef' for c in head):raise ValueError('invalid git head')
m={'jobs':o.get('_jobs',[]),'image_id':o.get('_image_id',''),'runtime':o.get('_runtime',''),'worker':worker,'head':head,'files':files,'patch_bytes':len(diff),'total_bytes':total+len(diff)}
canonical=json.dumps(m,sort_keys=True,separators=(',',':')).encode()+diff
m['id']=hashlib.sha256(canonical).hexdigest()
if op=='plan':print(json.dumps(m));raise SystemExit(0)
if o.get('expected_plan')!=m['id']:raise ValueError('artifact changed; review a new plan')
with tarfile.open(fileobj=sys.stdout.buffer,mode='w|',format=tarfile.USTAR_FORMAT) as archive:
    for name,data in [('manifest.json',json.dumps(m,sort_keys=True).encode()),('changes.patch',diff)]+[('files/'+f['path'],data) for f,data in zip(files,payload)]:
        item=tarfile.TarInfo(name);item.size=len(data);item.mode=0o600;archive.addfile(item,io.BytesIO(data))
`

func (m *Manager) artifact(ctx context.Context, id, op string, options ArtifactOptions, out io.Writer) error {
	if len(options.Paths) == 0 || len(options.Paths) > 256 || len(options.JobIDs) > 32 || options.MaxBytes < 0 || options.MaxBytes > 32<<20 {
		return fmt.Errorf("select 1–256 artifact files and at most 32 MiB")
	}
	for _, p := range options.Paths {
		if len(p) == 0 || len(p) > 94 || !utf8.ValidString(p) {
			return fmt.Errorf("artifact paths must contain 1–94 UTF-8 bytes")
		}
	}
	for _, id := range options.JobIDs {
		if len(id) == 0 || len(id) > 256 || strings.ContainsRune(id, 0) {
			return fmt.Errorf("invalid artifact job ID")
		}
	}
	if op == "bundle" && len(options.ExpectedPlan) != 64 {
		return fmt.Errorf("a reviewed artifact plan ID is required")
	}
	worker, err := m.cli.ContainerInspect(ctx, id)
	if err != nil {
		return err
	}
	if worker.Config == nil || worker.HostConfig == nil || worker.Config.Labels[labelPrefix+"managed"] != "true" {
		return fmt.Errorf("artifact requires a managed worker")
	}
	jobs := make([]Job, 0, len(options.JobIDs))
	seen := map[string]bool{}
	for _, jobID := range options.JobIDs {
		if jobID == "" || seen[jobID] {
			return fmt.Errorf("select distinct job IDs")
		}
		seen[jobID] = true
		job, err := m.Job(ctx, worker.ID, jobID)
		if err != nil {
			return err
		}
		jobs = append(jobs, job)
	}
	slices.SortFunc(jobs, func(a, b Job) int { return strings.Compare(a.ID, b.ID) })
	raw, err := json.Marshal(struct {
		ArtifactOptions
		ImageID string `json:"_image_id"`
		Runtime string `json:"_runtime"`
		Jobs    []Job  `json:"_jobs"`
	}{options, worker.Image, worker.HostConfig.Runtime, jobs})
	if err != nil {
		return err
	}
	if len(raw) > 64<<10 {
		return fmt.Errorf("artifact selection metadata exceeds 64 KiB")
	}
	limit := int64(1 << 20)
	if op == "bundle" {
		limit = options.MaxBytes
		if limit == 0 {
			limit = 32 << 20
		}
		limit += 1 << 20
	}
	if out == nil {
		return fmt.Errorf("artifact output writer is required")
	}
	bounded := &artifactOutput{out: out, remaining: limit}
	code, err := m.Exec(ctx, worker.ID, []string{"python3", "-I", "-c", artifactProgram, op, worker.ID, string(raw)}, nil, bounded, &artifactOutput{out: io.Discard, remaining: 64 << 10})
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("artifact %s failed (exit %d); check selected paths, size and plan ID", op, code)
	}
	return nil
}
func (m *Manager) PlanArtifacts(ctx context.Context, id string, o ArtifactOptions) (ArtifactPlan, error) {
	var out bytes.Buffer
	if err := m.artifact(ctx, id, "plan", o, &out); err != nil {
		return ArtifactPlan{}, err
	}
	var plan ArtifactPlan
	err := json.Unmarshal(out.Bytes(), &plan)
	return plan, err
}
func (m *Manager) ExportArtifacts(ctx context.Context, id string, o ArtifactOptions, out io.Writer) error {
	return m.artifact(ctx, id, "bundle", o, out)
}

// Enforce host limits independently of the guest collector or its interpreter.
type artifactOutput struct {
	out       io.Writer
	remaining int64
}

func (w *artifactOutput) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, fmt.Errorf("artifact output exceeds host limit")
	}
	n, err := w.out.Write(p)
	w.remaining -= int64(n)
	return n, err
}
