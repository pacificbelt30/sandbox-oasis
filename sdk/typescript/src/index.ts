import { spawn, type ChildProcessWithoutNullStreams } from 'node:child_process';
import type { MethodParams, MethodResults, Options, LaunchRequest, APIExecResult, APICapabilities } from './types.js';
export type * from './types.js';

type Method = keyof MethodParams;
export interface CallOptions { signal?: AbortSignal; timeoutMs?: number }
export interface Call<T> { id: number; result: Promise<T>; cancel: () => Promise<MethodResults['cancel']> }
const mutations = new Set<string>(['create','launch','start','stop','remove','remove_request','quarantine','exec']);
const codes = new Set(['invalid_argument','version_mismatch','conflict','cleanup_pending','outcome_unknown','request_gone','in_progress','capacity','not_found','canceled','deadline_exceeded','permission_denied','output_limit','not_initialized','busy','internal','transport_lost','protocol_error']);

export class APIError extends Error {
  readonly code: string;
  readonly codes: string[];
  readonly requiresReconciliation: boolean;
  // Intentionally non-enumerable: console.log(error) must not dump guest output.
  declare readonly partialResult: unknown;
  constructor(code: string, reconcile = false, partial?: unknown, categories?: string[]) {
    const safe = codes.has(code) ? code : 'protocol_error';
    super(safe);
    this.name = 'APIError'; this.code = safe; this.codes = categories ?? [safe]; this.requiresReconciliation = reconcile;
    Object.defineProperty(this, 'partialResult', { value: partial, enumerable: false });
  }
}
interface Pending { method: Method; resolve: (v: unknown) => void; reject: (e: APIError) => void; cleanup: () => void }

export class Client {
  private readonly child: ChildProcessWithoutNullStreams;
  private nextID = 0;
  private pending = new Map<number, Pending>();
  private buffer = Buffer.alloc(0);
  private closed = false;
  private capabilitiesValue?: APICapabilities;
  private closing?: Promise<void>;

  private constructor(binary: string) {
    // Options are written only to stdin, never argv or a host shell. Do not
    // forward stderr: underlying process diagnostics may contain private data.
    this.child = spawn(binary, ['api','stdio'], { stdio: ['pipe','pipe','pipe'] });
    this.child.stderr.resume();
    this.child.on('error', () => this.fail('transport_lost'));
    this.child.stdin.on('error', () => this.fail('transport_lost'));
    this.child.stdout.on('data', (data: Buffer) => this.read(data));
    this.child.stdout.on('error', () => this.fail('transport_lost'));
    this.child.on('close', () => this.fail('transport_lost'));
  }
  static async open(options: Options = {}, config: { binary?: string; signal?: AbortSignal } = {}): Promise<Client> {
    const client = new Client(config.binary ?? 'oaiso');
    try {
      const caps = await client.call('open', { options }, { signal: config.signal });
      if (caps.protocol_version !== 1 || caps.node.ProtocolVersion !== 2) throw new APIError('version_mismatch');
      client.capabilitiesValue = caps;
      return client;
    } catch (e) { await client.close(); throw e; }
  }
  get capabilities(): APICapabilities { return this.capabilitiesValue!; }

  submit<M extends Method>(method: M, params: MethodParams[M], options: CallOptions = {}): Call<MethodResults[M]> {
    if (this.closed) throw new APIError('transport_lost');
    if (options.signal?.aborted) throw new APIError('canceled');
    const timeout = options.timeoutMs ?? 180000;
    if (!Number.isSafeInteger(timeout) || timeout < 0 || timeout > 1200000) throw new APIError('invalid_argument');
    const id = this.nextID + 1;
    let data: string;
    try { data = JSON.stringify({ version: 1, id, method, params, timeout_ms: timeout }, (_key, value: unknown) => { if (typeof value === "number" && !Number.isSafeInteger(value)) throw new Error(); return value; }); }
    catch { throw new APIError('invalid_argument'); }
    if (Buffer.byteLength(data) > 1 << 20) throw new APIError('invalid_argument');
    let accept!: (v: unknown) => void; let reject!: (e: APIError) => void;
    const result = new Promise<MethodResults[M]>((resolve, fail) => { accept = v => resolve(v as MethodResults[M]); reject = fail; });
    const abort = () => {
      // Keep the pending slot until the response arrives: cancellation can
      // race a completed mutation and its late result must still be consumed.
      reject(new APIError('canceled', mutations.has(method)));
      try { void this.call('cancel', { call_id: id }).catch(() => {}); } catch { /* reconcile using a new client */ }
    };
    this.pending.set(id, { method, resolve: accept, reject, cleanup: () => options.signal?.removeEventListener('abort', abort) });
    this.nextID = id;
    options.signal?.addEventListener('abort', abort, { once: true });
    this.child.stdin.write(data + '\n', err => { if (err) this.fail('transport_lost'); });
    return { id, result, cancel: () => this.call('cancel', { call_id: id }) };
  }
  call<M extends Method>(method: M, params: MethodParams[M], options?: CallOptions): Promise<MethodResults[M]> {
    return this.submit(method, params, options).result;
  }
  private read(data: Buffer): void {
    this.buffer = Buffer.concat([this.buffer, data]);
    for (;;) {
      const end = this.buffer.indexOf(10);
      if (end < 0) { if (this.buffer.length > 4 << 20) this.fail('protocol_error'); return; }
      if (end > 4 << 20) { this.fail('protocol_error'); return; }
      const line = this.buffer.subarray(0, end); this.buffer = this.buffer.subarray(end + 1);
      let r: { version: number; id: number; result?: unknown; error?: {code: string; codes: string[]; requires_reconciliation: boolean} };
      try { r = JSON.parse(line.toString('utf8'), (_key, value: unknown) => { if (typeof value === 'number' && !Number.isSafeInteger(value)) throw new Error(); return value; }); } catch { this.fail('protocol_error'); return; }
      if (!r || r.version !== 1 || !Number.isSafeInteger(r.id) || !('result' in r || 'error' in r)) { this.fail('protocol_error'); return; }
      const p = this.pending.get(r.id);
      if (!p) { this.fail('protocol_error'); return; }
      this.pending.delete(r.id); p.cleanup();
      if (r.error) {
        const e = r.error;
        if (!codes.has(e.code) || !Array.isArray(e.codes) || e.codes.some(c => !codes.has(c)) || typeof e.requires_reconciliation !== 'boolean') {
          p.reject(new APIError('protocol_error', mutations.has(p.method))); this.fail('protocol_error'); return;
        }
        p.reject(new APIError(e.code, e.requires_reconciliation, r.result, e.codes));
      } else p.resolve(r.result);
    }
  }
  private fail(code: string): void {
    this.closed = true;
    const pending = this.pending; this.pending = new Map();
    for (const p of pending.values()) { p.cleanup(); p.reject(new APIError(code, mutations.has(p.method))); }
  }
  create(request: LaunchRequest, options?: CallOptions) { return this.call('create', { request }, options); }
  launch(request: LaunchRequest, options?: CallOptions) { return this.call('launch', { request }, options); }
  get(workerID: string, options?: CallOptions) { return this.call('get', { worker_id: workerID }, options); }
  list(options?: CallOptions) { return this.call('list', {}, options); }
  request(requestID: string, options?: CallOptions) { return this.call('request', { request_id: requestID }, options); }
  start(workerID: string, options?: CallOptions) { return this.call('start', { worker_id: workerID }, options); }
  stop(workerID: string, timeoutSeconds = 10, options?: CallOptions) { return this.call('stop', { worker_id: workerID, timeout_seconds: timeoutSeconds }, options); }
  remove(workerID: string, force = false, options?: CallOptions) { return this.call('remove', { worker_id: workerID, force }, options); }
  removeRequest(requestID: string, force = false, options?: CallOptions) { return this.call('remove_request', { request_id: requestID, force }, options); }
  wait(workerID: string, options?: CallOptions) { return this.call('wait', { worker_id: workerID }, options); }
  quarantine(workerID: string, options?: CallOptions) { return this.call('quarantine', { worker_id: workerID }, options); }
  profiles(options?: CallOptions) { return this.call('profiles', {}, options); }
  resourcePolicies(options?: CallOptions) { return this.call('resource_policies', {}, options); }
  resourceUsage(workerID: string, options?: CallOptions) { return this.call('resource_usage', { worker_id: workerID }, options); }
  proxyCapabilities(options?: CallOptions) { return this.call('proxy_capabilities', {}, options); }
  async exec(workerID: string, argv: string[], input: { stdin?: Uint8Array; env?: string[]; workingDir?: string } = {}, options?: CallOptions): Promise<Omit<APIExecResult, 'stdout' | 'stderr'> & { stdout: Buffer; stderr: Buffer }> {
    const r = await this.call('exec', { worker_id: workerID, argv, stdin: input.stdin === undefined ? undefined : Buffer.from(input.stdin).toString('base64'), env: input.env, working_dir: input.workingDir }, options);
    return { ...r, stdout: Buffer.from(r.stdout ?? '', 'base64'), stderr: Buffer.from(r.stderr ?? '', 'base64') };
  }
  close(): Promise<void> {
    if (this.closing) return this.closing;
    this.fail('transport_lost');
    this.child.stdin.end();
    this.closing = (async () => {
      if (this.child.exitCode !== null || this.child.signalCode !== null || !this.child.pid) return;
      let timer: ReturnType<typeof setTimeout>;
      await Promise.race([new Promise<void>(resolve => this.child.once('close', () => resolve())),
        new Promise<void>(resolve => { timer = setTimeout(resolve, 12000); })]);
      clearTimeout(timer!);
      if (this.child.exitCode !== null || this.child.signalCode !== null) return;
      this.child.kill('SIGTERM');
      await new Promise<void>(resolve => { const timer = setTimeout(() => { this.child.kill('SIGKILL'); resolve(); }, 5000); this.child.once('close', () => { clearTimeout(timer); resolve(); }); });
    })();
    return this.closing;
  }
}
