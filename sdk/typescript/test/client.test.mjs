import { test } from 'node:test';
import assert from 'node:assert/strict';
import { inspect } from 'node:util';
import { Client, APIError } from '../dist/index.js';

const binary = process.env.OAISO_SDK_TEST_BINARY;
assert.ok(binary, 'run scripts/verify-local-sdks.py with the Go fixture');
const secret = 'fixture-private-secret-must-never-be-serialized';
const open = () => Client.open({ ProxyAdminSecret: secret }, { binary });

test('lifecycle, exact replay, conflict, binary exec and durable cleanup', async t => {
  const c = await open(); t.after(() => c.close());
  assert.equal(c.capabilities.protocol_version, 1);
  const request = { RequestID: 'typescript/create-1', ProjectDir: '/workspace', Profile: 'compact', NoInternet: true };
  const w = await c.create(request);
  assert.equal((await c.create(request)).ID, w.ID);
  assert.equal((await c.list()).length, 1);
  await assert.rejects(c.create({ ...request, Image: 'different' }), e => e.code === 'conflict' && !e.requiresReconciliation);
  await c.start(w.ID); assert.equal((await c.get(w.ID)).Status, 'running');
  assert.equal((await c.request(request.RequestID)).WorkerID, w.ID);
  assert.ok((await c.profiles())[0].HostMemoryBytes > 0);
  assert.equal((await c.resourcePolicies())[0].pid_limit, 256);
  assert.equal((await c.resourceUsage(w.ID)).Worker, 'fixture');
  assert.equal((await c.proxyCapabilities()).WorkerMTLS, true);
  const result = await c.exec(w.ID, ['echo'], { stdin: Buffer.from([0,255,3]) });
  assert.equal(result.exit_code, 37); assert.deepEqual(result.stdout, Buffer.from([0,255,3]));
  assert.equal(result.stderr.toString(), 'fixture-stderr');
  assert.equal((await c.wait(w.ID)).exit_code, 37);
  await c.stop(w.ID, 0); await c.quarantine(w.ID);
  await c.removeRequest(request.RequestID, true); await c.removeRequest(request.RequestID, true);
  assert.deepEqual(await c.list(), []);
  await assert.rejects(c.create(request), e => e.code === 'request_gone');
});

test('lost response retains worker ID, redacts errors and replays once', async t => {
  const c = await open(); t.after(() => c.close());
  const request = { RequestID: 'lost-result', ProjectDir: '/workspace' };
  await assert.rejects(c.launch(request), e => {
    assert.ok(e instanceof APIError); assert.equal(e.code, 'outcome_unknown'); assert.equal(e.requiresReconciliation, true);
    assert.equal(e.partialResult.ID, 'lost-result');
    assert.ok(!inspect(e).includes(secret)); assert.ok(!JSON.stringify(e).includes(secret)); return true;
  });
  assert.equal((await c.request('lost-result')).State, 'complete');
  assert.equal((await c.launch(request)).ID, 'lost-result');
  assert.equal((await c.list()).length, 1); await c.removeRequest('lost-result', true);
});

test('explicit cancel preserves partial result and wait only cancels wait', async t => {
  const c = await open(); t.after(() => c.close());
  const pending = c.submit('launch', { request: { RequestID: 'block', ProjectDir: '/workspace' } });
  const rejected = assert.rejects(pending.result, e => e.code === 'canceled' && e.requiresReconciliation && (!e.partialResult || e.partialResult.ID === 'partial-worker'));
  assert.equal((await pending.cancel()).accepted, true); await rejected;
  const waiting = c.submit('wait', { worker_id: 'block' });
  const waitRejected = assert.rejects(waiting.result, e => e.code === 'canceled' && !e.requiresReconciliation);
  await waiting.cancel(); await waitRejected; assert.ok((await c.profiles()).length);
});

test('AbortSignal and server deadlines classify uncertain exec outcomes', async t => {
  const c = await open(); t.after(() => c.close());
  const controller = new AbortController();
  const command = c.exec('worker', ['block'], {}, { signal: controller.signal });
  const rejected = assert.rejects(command, e => e.code === 'canceled' && e.requiresReconciliation);
  controller.abort(); await rejected;
  await assert.rejects(c.exec('worker', ['block'], {}, { timeoutMs: 10 }), e => e.code === 'deadline_exceeded' && e.requiresReconciliation);
  await assert.rejects(c.exec('worker', ['overflow']), e => e.codes.includes('output_limit') && e.requiresReconciliation);
  await assert.rejects(c.exec('worker', ['private-error'], {env:['PRIVATE='+secret]}), e => !inspect(e).includes(secret));
});

test('version mismatch fails before use', async () => {
  await assert.rejects(Client.open({ NodeSocket: '/version-mismatch', ProxyAdminSecret: secret }, { binary }), e => e.code === 'version_mismatch' && !inspect(e).includes(secret));
});

test('concurrent calls keep numeric correlation', async t => {
  const c = await open(); t.after(() => c.close());
  const workers = await Promise.all(Array.from({length:8}, (_,i) => c.create({RequestID:`call-${i}`,ProjectDir:'/workspace'})));
  assert.deepEqual(workers.map(w => w.ID), Array.from({length:8},(_,i) => `call-${i}`));
});

test('unsafe/non-finite integers fail before sending a mutation', async t => {
  const c = await open(); t.after(() => c.close());
  for (const timeout_seconds of [NaN, Infinity, 9007199254740992]) {
    assert.throws(() => c.submit('stop', {worker_id:'worker', timeout_seconds}), e => e.code === 'invalid_argument' && !e.requiresReconciliation);
  }
  assert.ok((await c.profiles()).length);
});

test('permission failures are fixed categories and transport loss is uncertain', async t => {
  await assert.rejects(Client.open({NodeSocket:'/permission-denied'}, {binary}), e => e.code === 'permission_denied' && !inspect(e).includes(secret));
  const c = await open(); t.after(() => c.close());
  const command = c.submit('exec',{worker_id:'worker',argv:['block']});
  const failed = assert.rejects(command.result,e => e.code === 'transport_lost' && e.requiresReconciliation);
  c.child.kill('SIGKILL'); await failed;
});

test('absent stdin and explicit empty EOF preserve Go semantics', async t => {
  const c = await open(); t.after(() => c.close());
  assert.equal((await c.exec('worker',['stdin-mode'])).stdout.toString(),'absent');
  assert.equal((await c.exec('worker',['stdin-mode'],{stdin:Buffer.alloc(0)})).stdout.toString(),'empty');
  assert.equal((await c.exec('worker',['stdin-mode'],{stdin:Buffer.from('data')})).stdout.toString(),'present');
});
