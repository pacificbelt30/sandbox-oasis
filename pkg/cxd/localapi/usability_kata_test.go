package localapi_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/docker/docker/api/types/container"
	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/pkg/cxd"
	"github.com/pacificbelt30/codex-dock/pkg/cxd/identity"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testKataUsability(t *testing.T, ctx context.Context, root string, options cxd.Options) {
	project := filepath.Join(root, "usability")
	if err := os.Mkdir(project, 0755); err != nil {
		t.Fatal(err)
	}
	for _, argv := range [][]string{{"init"}, {"config", "user.name", "Fixture"}, {"config", "user.email", "fixture@example.invalid"}} {
		cmd := exec.CommandContext(ctx, "git", argv...)
		cmd.Dir = project
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(project, "tracked"), []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, "git", "-c", "commit.gpgsign=false", "add", "tracked")
	cmd.Dir = project
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v %s", err, out)
	}
	cmd = exec.CommandContext(ctx, "git", "-c", "commit.gpgsign=false", "commit", "-m", "fixture")
	cmd.Dir = project
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v %s", err, out)
	}
	options.StateDir = filepath.Join(root, "usability-state")
	options.EgressProxyImage = os.Getenv("CODEX_DOCK_KATA_API_PROXY_IMAGE")
	options.EgressPolicy = cxd.EgressPolicy{DefaultDeny: true, AllowCIDRs: []string{"203.0.113.1/32"}, AllowPorts: []int{80, 443}}
	client, err := cxd.Open(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	requestID := fmt.Sprintf("usability/%d", time.Now().UnixNano())
	req := cxd.LaunchRequest{RequestID: requestID, ProjectDir: project, Image: os.Getenv("CODEX_DOCK_KATA_TEST_IMAGE"), Profile: "compact", User: fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), Identity: identity.Identity{CaseID: "sdk-case", RunID: requestID, ConnectionVersion: "1", Provider: "openai"}}
	worker, err := client.Launch(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := client.RemoveRequest(cleanup, requestID, true); err != nil {
			t.Error(err)
		}
	}()
	selected, err := client.Policy(ctx, worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupRequest, stopRequest := context.WithTimeout(context.Background(), time.Minute)
		_ = client.RemoveRequest(cleanupRequest, requestID, true)
		stopRequest()
		cli, err := engine.NewClientAt(engine.Socket())
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = cli.Close() }()
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = cli.ContainerStop(cleanup, selected.Proxy, container.StopOptions{})
		if err := cli.ContainerRemove(cleanup, selected.Proxy, container.RemoveOptions{Force: true}); err != nil {
			t.Error(err)
		}
	}()
	if selected.Generation != 1 {
		t.Fatal("live egress control unavailable", selected.Generation)
	}
	if err = client.WriteFile(ctx, worker.ID, "tracked", strings.NewReader("after")); err != nil {
		t.Fatal(err)
	}
	plan, err := client.PlanArtifacts(ctx, worker.ID, cxd.ArtifactOptions{Paths: []string{"tracked"}})
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err = client.ExportArtifacts(ctx, worker.ID, cxd.ArtifactOptions{Paths: []string{"tracked"}, ExpectedPlan: plan.ID}, &archive); err != nil {
		t.Fatal(err)
	}
	if archive.Len() == 0 || !bytes.Contains(archive.Bytes(), []byte("changes.patch")) {
		t.Fatal("artifact missing")
	}
	t.Log("verified artifact plan/export")
	_, err = client.StartJob(ctx, worker.ID, cxd.ExecRequest{Argv: []string{"python3", "-c", kataPreviewServer}})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	code, err := client.Exec(ctx, worker.ID, cxd.ExecRequest{Argv: []string{"python3", "-c", `import urllib.request,time
for i in range(100):
 try:urllib.request.urlopen('http://127.0.0.1:8765/tracked').read();break
 except OSError:time.sleep(.1)
else:raise SystemExit(1)`}, Stdout: &output, Stderr: &output})
	if err != nil || code != 0 {
		t.Fatal("preview server readiness", code, err, output.String())
	}
	preview, err := client.Preview(ctx, worker.ID, 8765, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = preview.Close() }()
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, Timeout: 15 * time.Second}
	resp, err := browser.Get(preview.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	origin := strings.Split(preview.URL, "/__oaiso_login")[0]
	resp, err = browser.Get(origin + "/tracked")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || string(data) != "after" {
		t.Fatal("preview body", string(data), err)
	}
	unauthorized, err := http.Get(origin + "/tracked")
	if err != nil {
		t.Fatal(err)
	}
	_ = unauthorized.Body.Close()
	if unauthorized.StatusCode != 403 {
		t.Fatal("preview allowed missing authentication")
	}
	badOrigin, _ := http.NewRequestWithContext(ctx, "GET", origin+"/tracked", nil)
	badOrigin.Header.Set("Origin", "https://attacker.invalid")
	resp, err = browser.Do(badOrigin)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("preview allowed foreign origin")
	}
	resp, err = browser.Get(origin + "/cookie")
	if err != nil {
		t.Fatal(err)
	}
	cookieBody, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || strings.Contains(string(cookieBody), "oaiso_preview_") {
		t.Fatal("preview cookie reached guest")
	}
	previewURL, _ := url.Parse(origin)
	ws, err := net.DialTimeout("tcp", previewURL.Host, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	_ = ws.SetDeadline(time.Now().Add(30 * time.Second))
	upgrade, _ := http.NewRequest("GET", origin+"/ws", nil)
	upgrade.Header.Set("Connection", "Upgrade")
	upgrade.Header.Set("Upgrade", "websocket")
	upgrade.Header.Set("Sec-WebSocket-Version", "13")
	upgrade.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	upgrade.Header.Set("Origin", origin)
	for _, cookie := range jar.Cookies(previewURL) {
		upgrade.AddCookie(cookie)
	}
	if err = upgrade.Write(ws); err != nil {
		t.Fatal(err)
	}
	wsReader := bufio.NewReader(ws)
	upgraded, err := http.ReadResponse(wsReader, upgrade)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Body.Close()
	if upgraded.StatusCode != 101 {
		t.Fatal("preview websocket refused", upgraded.StatusCode)
	}
	echo := func() {
		t.Helper()
		if _, err := ws.Write([]byte{0x81, 0x84, 0, 0, 0, 0, 'p', 'i', 'n', 'g'}); err != nil {
			t.Fatal(err)
		}
		data := make([]byte, 6)
		if _, err := io.ReadFull(wsReader, data); err != nil || !bytes.Equal(data, []byte{0x81, 4, 'p', 'i', 'n', 'g'}) {
			t.Fatal("preview websocket echo", err)
		}
	}
	expiring, err := client.Preview(ctx, worker.ID, 8765, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-expiring.Done():
	case <-time.After(6 * time.Second):
		_ = expiring.Close()
		t.Fatal("preview lifetime did not close listener")
	}
	expiredOrigin := strings.Split(expiring.URL, "/__oaiso_login")[0]
	expiredClient := &http.Client{Timeout: time.Second}
	if expired, err := expiredClient.Get(expiredOrigin); err == nil {
		_ = expired.Body.Close()
		t.Fatal("expired preview listener still accepts requests")
	}
	echo()
	t.Log("verified preview authentication, origin, cookie exclusion and WebSocket")
	// Hold plain HTTP and CONNECT in the actual guest. Only the upstream chain is synthetic.
	job, err := client.StartJob(ctx, worker.ID, cxd.ExecRequest{Argv: []string{"python3", "-c", kataLiveStreams}})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(project, "streams-ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			logs, _ := client.JobLogs(ctx, worker.ID, job.ID, 0)
			t.Fatal("egress streams not ready", logs.Output)
		}
		time.Sleep(100 * time.Millisecond)
	}
	change, err := client.PlanPolicyChange(ctx, worker.ID, cxd.EgressPolicy{DefaultDeny: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(change.Workers) != 1 || change.Workers[0] != worker.ID {
		t.Fatal("affected worker list", change.Workers)
	}
	proposalPath := filepath.Join(root, "policy-proposal.json")
	proposalData, _ := json.Marshal(map[string]any{"based_on_policy": selected.PolicyID, "rules": change.Proposed, "notice": "explicit fixture approval"})
	if err = os.WriteFile(proposalPath, proposalData, 0600); err != nil {
		t.Fatal(err)
	}
	planCommand := exec.CommandContext(ctx, os.Getenv("CODEX_DOCK_KATA_TEST_BINARY"), "policy", "plan", "--worker", worker.ID, "--file", proposalPath)
	planOutput, err := planCommand.Output()
	if err != nil {
		t.Fatal("CLI policy plan", err)
	}
	var cliPlan cxd.PolicyChangePlan
	if err = json.Unmarshal(planOutput, &cliPlan); err != nil || cliPlan.ID != change.ID {
		t.Fatal("CLI/Go policy plans differ", err)
	}
	applyCommand := exec.CommandContext(ctx, os.Getenv("CODEX_DOCK_KATA_TEST_BINARY"), "policy", "apply", "--worker", worker.ID, "--file", proposalPath, "--plan-id", cliPlan.ID)
	appliedOutput, err := applyCommand.Output()
	if err != nil {
		t.Fatal("CLI policy apply", err)
	}
	var state cxd.LivePolicyState
	err = json.Unmarshal(appliedOutput, &state)
	if err != nil || !state.Ready || state.Generation != 2 {
		t.Fatal("live apply", state, err)
	}
	ended, err := client.WaitJob(ctx, worker.ID, job.ID)
	if err != nil || ended.ExitCode != 0 {
		logs, _ := client.JobLogs(ctx, worker.ID, job.ID, 0)
		t.Fatal("stream invalidation", ended, err, logs.Output)
	}
	resultOptions := cxd.ArtifactOptions{Paths: []string{"tracked"}, JobIDs: []string{job.ID}}
	resultPlan, err := client.PlanArtifacts(ctx, worker.ID, resultOptions)
	if err != nil || len(resultPlan.Jobs) != 1 || resultPlan.Jobs[0].Running || resultPlan.Jobs[0].ExitCode != 0 {
		t.Fatal("artifact job result", resultPlan, err)
	}
	resultOptions.ExpectedPlan = resultPlan.ID
	var results bytes.Buffer
	if err = client.ExportArtifacts(ctx, worker.ID, resultOptions, &results); err != nil {
		t.Fatal(err)
	}
	echo()
	t.Log("verified live HTTP/CONNECT invalidation; preview remains available")
	if _, err = client.ApplyPolicyChange(ctx, worker.ID, cxd.EgressPolicy{DefaultDeny: true}, change.ID); err == nil {
		t.Fatal("stale plan accepted")
	}
	if err = client.Stop(ctx, worker.ID, 0); err != nil {
		t.Fatal(err)
	}
	select {
	case <-preview.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("preview survived worker stop")
	}
	if _, err := wsReader.ReadByte(); err == nil {
		t.Fatal("preview WebSocket survived worker stop")
	}
	// Use the actual Python SDK to capture, then the TypeScript SDK to restore.
	cfg, _ := json.Marshal(map[string]any{"options": options, "worker": worker.ID, "binary": os.Getenv("CODEX_DOCK_KATA_TEST_BINARY")})
	capture := exec.CommandContext(ctx, "python3", "-c", `import sys,json
from oaiso import Client
cfg=json.load(sys.stdin)
with Client(cfg['options'],binary=cfg['binary']) as c:
 saved=c.checkpoint(cfg['worker']);assert c.inspect_checkpoint(saved['id'])['id']==saved['id'];print(json.dumps(saved))`)
	capture.Env = append(os.Environ(), "PYTHONPATH="+filepath.Join(os.Getenv("OAISO_LOCAL_SDK_ASSETS"), "python"))
	capture.Stdin = bytes.NewReader(cfg)
	captured, err := capture.Output()
	if err != nil {
		t.Fatal("Python SDK checkpoint", err)
	}
	var saved cxd.DiskCheckpoint
	if err = json.Unmarshal(captured, &saved); err != nil {
		t.Fatal(err)
	}
	t.Log("verified Python SDK checkpoint capture/inspect")
	if err != nil {
		t.Fatal(err)
	}
	removedCheckpoint := false
	defer func() {
		if removedCheckpoint {
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := client.RemoveCheckpoint(cleanup, saved.ID); err != nil {
			t.Error(err)
		}
	}()
	restoreID := requestID + "/restore"
	restoreReq := req
	restoreReq.RequestID = restoreID
	restoreReq.Image = ""
	restoreReq.NoInternet = true
	restoreReq.Identity.RunID = restoreID
	restoreCfg, _ := json.Marshal(map[string]any{"options": options, "request": restoreReq, "id": saved.ID, "binary": os.Getenv("CODEX_DOCK_KATA_TEST_BINARY")})
	restoreScript := filepath.Join(root, "restore.mjs")
	restoreSource := `import fs from 'node:fs';
import {Client} from '` + filepath.Join(os.Getenv("OAISO_LOCAL_SDK_ASSETS"), "typescript/dist/index.js") + `';
const cfg=JSON.parse(fs.readFileSync(0,'utf8'));
const c=await Client.open(cfg.options,{binary:cfg.binary});
try {
 const saved=await c.inspectCheckpoint(cfg.id);if(saved.id!==cfg.id)throw Error('checkpoint mismatch');
 const w=await c.restoreCheckpoint(cfg.id,cfg.request);
 const r=await c.exec(w.ID,['printf','restored']);if(r.exit_code!==0 || r.stdout.toString()!=='restored')throw Error('restore failed');
 await c.removeRequest(cfg.request.RequestID,true);
} finally {await c.close();}`
	if err = os.WriteFile(restoreScript, []byte(restoreSource), 0600); err != nil {
		t.Fatal(err)
	}
	restore := exec.CommandContext(ctx, filepath.Join(os.Getenv("OAISO_LOCAL_SDK_ASSETS"), "node"), restoreScript)
	restore.Stdin = bytes.NewReader(restoreCfg)
	if output, err := restore.CombinedOutput(); err != nil {
		cleanup, stop := context.WithTimeout(context.Background(), time.Minute)
		_ = client.RemoveRequest(cleanup, restoreID, true)
		stop()
		t.Fatalf("TypeScript SDK restore: %v %s", err, output)
	}
	t.Log("verified TypeScript SDK checkpoint inspect/restore and execution")
	if err = client.PinCheckpoint(ctx, saved.ID, true); err != nil {
		t.Fatal(err)
	}
	retention, err := client.RetainCheckpoints(ctx, cxd.CheckpointRetentionOptions{KeepBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(retention.Delete) != 0 || len(retention.Protected) != 1 {
		t.Fatal("pinned checkpoint deletion planned", retention)
	}
	if err = client.PinCheckpoint(ctx, saved.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err = client.RetainCheckpoints(ctx, cxd.CheckpointRetentionOptions{KeepBytes: 1, Apply: true, ExpectedPlan: retention.ID}); err == nil {
		t.Fatal("stale retention plan accepted")
	}
	stageID := requestID + "/retained"
	staged := restoreReq
	staged.RequestID = stageID
	staged.Identity.RunID = stageID
	staged.Image = saved.Image
	staged.Environment.ImageID = saved.Digest
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), time.Minute)
		_ = client.RemoveRequest(cleanup, stageID, true)
		stop()
	}()
	if _, err = client.Create(ctx, staged); err != nil {
		t.Fatal("create retained checkpoint reference", err)
	}
	retained, err := client.RetainCheckpoints(ctx, cxd.CheckpointRetentionOptions{KeepBytes: 1})
	if err != nil || len(retained.Delete) != 0 || len(retained.Protected) != 1 {
		t.Fatal("referenced checkpoint selected", retained, err)
	}
	if err = client.RemoveRequest(ctx, stageID, true); err != nil {
		t.Fatal(err)
	}
	retention, err = client.RetainCheckpoints(ctx, cxd.CheckpointRetentionOptions{KeepBytes: 1})
	if err != nil || len(retention.Delete) != 1 {
		t.Fatal("retention plan", retention, err)
	}
	if retention.Delete[0] != saved.ID {
		t.Fatal("refusing to delete a checkpoint not created by this test")
	}
	applied, err := client.RetainCheckpoints(ctx, cxd.CheckpointRetentionOptions{KeepBytes: 1, Apply: true, ExpectedPlan: retention.ID})
	if err != nil || len(applied.Removed) != 1 || applied.Removed[0] != saved.ID {
		t.Fatal("retention application", applied, err)
	}
	removedCheckpoint = true
	catalog, err := client.Checkpoints(ctx)
	if err != nil || len(catalog.Items) != 0 {
		t.Fatal("checkpoint remained", catalog, err)
	}
	t.Log("PASS: artifact plan/export, authenticated preview, live HTTP/CONNECT invalidation, generation conflict, checkpoint pins/retention")
}

const kataLiveStreams = `import socket,os,urllib.parse,http.client,threading
from pathlib import Path
p=urllib.parse.urlparse(os.environ['HTTP_PROXY'])
http=http.client.HTTPConnection(p.hostname,p.port,timeout=15);http.request('GET','http://203.0.113.1/stream');response=http.getresponse();assert response.status==200;assert response.read(6)==b'ready\n'
tunnel=socket.create_connection((p.hostname,p.port),timeout=15);tunnel.sendall(b'CONNECT 203.0.113.1:443 HTTP/1.1\r\nHost: 203.0.113.1:443\r\n\r\n')
data=b''
while b'\r\n\r\n' not in data:data+=tunnel.recv(4096)
assert b'200' in data;tunnel.sendall(b'echo');assert tunnel.recv(4)==b'echo'
Path('/workspace/streams-ready').write_text('ready')
errors=[]
def drain():
 try:
  while response.read(65536):pass
 except (http.client.IncompleteRead,OSError):pass
 except Exception as e:errors.append(type(e).__name__)
t=threading.Thread(target=drain);t.start()
try:
 while tunnel.recv(4096):pass
except OSError:pass
t.join(timeout=15);assert not t.is_alive() and not errors
`

const kataPreviewServer = `import http.server,hashlib,base64
class H(http.server.SimpleHTTPRequestHandler):
    protocol_version='HTTP/1.1'
    def log_message(self,*args):pass
    def do_GET(self):
        if self.path=='/cookie':
            data=self.headers.get('Cookie','').encode();self.send_response(200);self.send_header('Content-Length',str(len(data)));self.end_headers();self.wfile.write(data);return
        if self.path=='/ws':
            key=self.headers['Sec-WebSocket-Key'];accept=base64.b64encode(hashlib.sha1((key+'258EAFA5-E914-47DA-95CA-C5AB0DC85B11').encode()).digest()).decode()
            self.send_response(101);self.send_header('Upgrade','websocket');self.send_header('Connection','Upgrade');self.send_header('Sec-WebSocket-Accept',accept);self.end_headers()
            while True:
                head=self.rfile.read(2)
                if len(head)!=2:break
                size=head[1]&127
                if size>125:break
                mask=self.rfile.read(4);data=self.rfile.read(size)
                if len(mask)!=4 or len(data)!=size:break
                data=bytes(b^mask[i%4] for i,b in enumerate(data));self.wfile.write(bytes([129,len(data)])+data)
            return
        super().do_GET()
http.server.ThreadingHTTPServer(('127.0.0.1',8765),H).serve_forever()
`
