import fs from 'node:fs';
import { spawn } from 'node:child_process';
import assert from 'node:assert/strict';
import { Client } from '../dist/index.js';

const cfg = JSON.parse(fs.readFileSync(0, 'utf8'));
const request = cfg.request;
const c = await Client.open(cfg.options, {binary: cfg.binary});
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
try {
  assert.equal(c.capabilities.node.ProtocolVersion, 2);
  assert.ok((await c.profiles()).length); assert.ok((await c.resourcePolicies()).length);
  const w = await c.create(request);
  assert.equal((await c.create(request)).ID, w.ID);
  await assert.rejects(c.create({...request, ReadOnly:true}), e => e.code === 'conflict', 'changed request must conflict');
  assert.equal((await c.request(request.RequestID)).State, 'complete');
  await c.start(w.ID); assert.equal((await c.get(w.ID)).Status, 'running');
  const result = await c.exec(w.ID, ['python3','-c','import sys;sys.stdout.buffer.write(sys.stdin.buffer.read());sys.stderr.write("stderr-ok");sys.exit(7)'], {stdin:Buffer.from([0,255,42])});
  assert.equal(result.exit_code, 7); assert.deepEqual(result.stdout, Buffer.from([0,255,42])); assert.equal(result.stderr.toString(), 'stderr-ok');
  const usage = await c.resourceUsage(w.ID); assert.equal(usage.Profile.Name, 'compact'); assert.ok(usage.HostMemoryCurrentBytes > 0);
  const probe = await c.exec(w.ID, ['python3','-c','import json,os;from pathlib import Path;p=Path.home();print(json.dumps(dict(os.environ)));[(print(f.read_text())) for d in (p/".codex",p/".claude") if d.is_dir() for f in d.rglob("*.json") if f.stat().st_size<100000]']);
  console.log('Verified TypeScript create/start/exec/resources');
  for (const secret of cfg.secrets) assert.ok(!probe.stdout.includes(secret) && !probe.stderr.includes(secret), 'guest credential leak');
  const waiting = c.submit('wait', {worker_id:w.ID});
  const canceledWait = assert.rejects(waiting.result, e => e.code === 'canceled' && !e.requiresReconciliation, 'wait cancellation must reject');
  await waiting.cancel(); await canceledWait; assert.equal((await c.get(w.ID)).Status, 'running');
  console.log('Verified TypeScript wait cancellation');
  const heartbeat = request.ProjectDir + '/typescript-heartbeat';
  const command = c.submit('exec', {worker_id:w.ID, argv:['python3','-c','import time;from pathlib import Path\np=Path("/workspace/typescript-heartbeat")\nwhile True: p.write_text(str(time.time_ns()));time.sleep(.05)']});
  const canceledCommand = assert.rejects(command.result, e => e.code === 'canceled' && e.requiresReconciliation, 'exec cancellation must reject');
  const deadline = Date.now() + 30000;
  while (!fs.existsSync(heartbeat)) { assert.ok(Date.now() < deadline, 'exec never started'); await delay(50); }
  await command.cancel(); await canceledCommand; await delay(200);
  const before = fs.readFileSync(heartbeat, 'utf8'); await delay(300); assert.equal(fs.readFileSync(heartbeat, 'utf8'), before, 'canceled guest process still runs');
  console.log('Verified TypeScript exec cancellation');
  const lost = {...request, RequestID:cfg.lost_request_id};
  const child = spawn(cfg.binary, ['api','stdio'], {stdio:['pipe','pipe','ignore']});
  const write = r => child.stdin.write(JSON.stringify(r) + '\n');
  try {
    let first = '';
    await new Promise((resolve, reject) => {
      const read = data => { first += data.toString(); if (first.includes('\n')) { child.stdout.off('data',read); child.stdout.pause(); resolve(); } };
      child.stdout.on('data',read); child.on('error', reject);
      write({version:1,id:1,method:'open',params:{options:cfg.options}});
    });
    assert.ok(!JSON.parse(first.trim()).error);
    write({version:1,id:2,method:'create',params:{request:lost}});
    const deadline = Date.now() + 60000;
    for (;;) {
      try { if ((await c.request(lost.RequestID)).State === 'complete') break; }
      catch (e) { assert.equal(e.code, 'not_found'); }
      assert.ok(Date.now() < deadline, 'lost request never completed'); await delay(100);
    }
    child.kill('SIGKILL'); await new Promise(resolve => child.once('close',resolve));
  } finally { if (child.exitCode === null && child.signalCode === null) child.kill('SIGKILL'); }
  const recovered = await c.create(lost); assert.equal(recovered.ID, (await c.request(lost.RequestID)).WorkerID);
  await c.removeRequest(lost.RequestID,true); await c.removeRequest(lost.RequestID,true);
  await c.stop(w.ID,0); assert.equal((await c.get(w.ID)).Status,'exited');
  await c.removeRequest(request.RequestID,true); await c.removeRequest(request.RequestID,true);
  await assert.rejects(c.create(request),e => e.code === 'request_gone');
} finally { await c.close(); }
await assert.rejects(Client.open(cfg.options,{binary:cfg.mismatched_binary}), e => e.code === 'version_mismatch');
console.log('PASS: TypeScript real Kata lifecycle, replay, lost response, cancellation, secret exclusion, resource usage and cleanup');
