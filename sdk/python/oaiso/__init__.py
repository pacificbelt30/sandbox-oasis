"""Oaiso local API v1. No runtime dependencies and no automatic retries."""
from concurrent.futures import Future, TimeoutError as FutureTimeout
import base64
import json
import os
import subprocess
import threading

_VERSION = 1
_MAX_REQUEST = 1 << 20
_MAX_RESPONSE = 4 << 20
_MUTATIONS = frozenset({"create", "launch", "start", "stop", "remove", "remove_request", "quarantine", "exec", "write_file", "start_job", "kill_job", "checkpoint", "remove_checkpoint"})
_CODES = frozenset({"invalid_argument", "version_mismatch", "conflict", "cleanup_pending", "outcome_unknown", "request_gone", "in_progress", "capacity", "not_found", "canceled", "deadline_exceeded", "permission_denied", "output_limit", "not_initialized", "busy", "internal", "transport_lost", "protocol_error"})


class APIError(Exception):
    """Fixed category only in str/repr. Inspect partial_result explicitly."""
    def __init__(self, code, *, codes=None, requires_reconciliation=False, partial_result=None):
        self.code = code if code in _CODES else "protocol_error"
        self.codes = tuple(codes or (self.code,))
        self.requires_reconciliation = requires_reconciliation
        self.partial_result = partial_result
        super().__init__(self.code)


class Call:
    def __init__(self, client, call_id, method, future):
        self.id = call_id
        self._client, self._method, self._future = client, method, future

    def result(self, timeout=None):
        try:
            return self._future.result(timeout)
        except FutureTimeout:
            try:
                self.cancel()
            except APIError:
                pass
            raise APIError("deadline_exceeded", requires_reconciliation=self._method in _MUTATIONS) from None

    def cancel(self):
        """Request cancellation; accepted does not prove stop or cleanup."""
        return self._client.submit("cancel", {"call_id": self.id})


class Client:
    def __init__(self, options=None, *, binary="oaiso", open_timeout=180):
        self._lock = threading.Lock()
        self._write_lock = threading.Lock()
        self._next_id = 0
        self._pending = {}
        self._closed = False
        # No options, secrets or shell expansions appear in child argv. The
        # protocol is private to these pipes; stderr is never forwarded.
        try:
            self._process = subprocess.Popen([os.fspath(binary), "api", "stdio"], stdin=subprocess.PIPE,
                                             stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
        except OSError:
            raise APIError("transport_lost") from None
        self._reader = threading.Thread(target=self._read, daemon=True)
        self._reader.start()
        try:
            self.capabilities = self.call("open", {"options": options or {}}, timeout=open_timeout)
            if (self.capabilities.get("protocol_version") != _VERSION or
                    self.capabilities.get("node", {}).get("ProtocolVersion") != 2):
                raise APIError("version_mismatch")
        except BaseException:
            self.close()
            raise

    def submit(self, method, params=None, *, timeout_ms=180000):
        if not isinstance(method, str) or type(timeout_ms) is not int or not 0 <= timeout_ms <= 1200000:
            raise APIError("invalid_argument")
        # This lock keeps numeric call IDs in wire order across caller threads.
        with self._write_lock:
            with self._lock:
                if self._closed:
                    raise APIError("transport_lost", requires_reconciliation=False)
                call_id = self._next_id + 1
            try:
                frame = json.dumps({"version": _VERSION, "id": call_id, "method": method,
                                    "params": {} if params is None else params, "timeout_ms": timeout_ms},
                                   ensure_ascii=True, allow_nan=False, separators=(",", ":")).encode() + b"\n"
            except (TypeError, ValueError, OverflowError):
                raise APIError("invalid_argument") from None
            if len(frame) - 1 > _MAX_REQUEST:
                raise APIError("invalid_argument")
            future = Future()
            with self._lock:
                if self._closed:
                    raise APIError("transport_lost")
                self._next_id = call_id
                self._pending[call_id] = (future, method)
            try:
                self._process.stdin.write(frame)
                self._process.stdin.flush()
            except (OSError, ValueError):
                self._fail("transport_lost")
            return Call(self, call_id, method, future)

    def call(self, method, params=None, *, timeout=None, timeout_ms=180000):
        return self.submit(method, params, timeout_ms=timeout_ms).result(timeout)

    def _read(self):
        try:
            while True:
                line = self._process.stdout.readline(_MAX_RESPONSE + 2)
                if not line:
                    self._fail("transport_lost")
                    return
                if len(line) > _MAX_RESPONSE + 1 or not line.endswith(b"\n"):
                    self._fail("protocol_error")
                    return
                r = json.loads(line)
                if (not isinstance(r, dict) or r.get("version") != _VERSION or type(r.get("id")) is not int
                        or ("result" not in r and "error" not in r)):
                    self._fail("protocol_error")
                    return
                with self._lock:
                    item = self._pending.pop(r["id"], None)
                if item is None:
                    self._fail("protocol_error")
                    return
                future, method = item
                e = r.get("error")
                if e is not None:
                    if (not isinstance(e, dict) or not isinstance(e.get("code"), str) or e.get("code") not in _CODES or
                            type(e.get("requires_reconciliation")) is not bool or
                            not isinstance(e.get("codes"), list) or
                            any(not isinstance(code, str) or code not in _CODES for code in e["codes"])):
                        future.set_exception(APIError("protocol_error", requires_reconciliation=method in _MUTATIONS))
                        self._fail("protocol_error")
                        return
                    future.set_exception(APIError(e["code"], codes=e["codes"],
                                                  requires_reconciliation=e["requires_reconciliation"],
                                                  partial_result=r.get("result")))
                else:
                    future.set_result(r["result"])
        except (OSError, ValueError, TypeError, KeyError):
            self._fail("protocol_error")

    def _fail(self, code):
        with self._lock:
            self._closed = True
            pending, self._pending = self._pending, {}
        for future, method in pending.values():
            future.set_exception(APIError(code, requires_reconciliation=method in _MUTATIONS))

    def create(self, request, **kwargs):
        return self.call("create", {"request": request}, **kwargs)

    def launch(self, request, **kwargs):
        return self.call("launch", {"request": request}, **kwargs)

    def get(self, worker_id, **kwargs):
        return self.call("get", {"worker_id": worker_id}, **kwargs)

    def list(self, **kwargs):
        return self.call("list", **kwargs)

    def request(self, request_id, **kwargs):
        return self.call("request", {"request_id": request_id}, **kwargs)

    def start(self, worker_id, **kwargs):
        return self.call("start", {"worker_id": worker_id}, **kwargs)

    def stop(self, worker_id, timeout_seconds=10, **kwargs):
        return self.call("stop", {"worker_id": worker_id, "timeout_seconds": timeout_seconds}, **kwargs)

    def remove(self, worker_id, force=False, **kwargs):
        return self.call("remove", {"worker_id": worker_id, "force": force}, **kwargs)

    def remove_request(self, request_id, force=False, **kwargs):
        return self.call("remove_request", {"request_id": request_id, "force": force}, **kwargs)

    def exec(self, worker_id, argv, *, stdin=None, env=None, working_dir="", **kwargs):
        result = self.call("exec", {"worker_id": worker_id, "argv": argv, "stdin": None if stdin is None else base64.b64encode(stdin).decode(),
                                   "env": env or [], "working_dir": working_dir}, **kwargs)
        return {**result, "stdout": base64.b64decode(result["stdout"] or "", validate=True),
                "stderr": base64.b64decode(result["stderr"] or "", validate=True)}

    def wait(self, worker_id, **kwargs):
        return self.call("wait", {"worker_id": worker_id}, **kwargs)

    def profiles(self, **kwargs):
        return self.call("profiles", **kwargs)

    def resource_policies(self, **kwargs):
        return self.call("resource_policies", **kwargs)

    def resource_usage(self, worker_id, **kwargs):
        return self.call("resource_usage", {"worker_id": worker_id}, **kwargs)

    def quarantine(self, worker_id, **kwargs):
        return self.call("quarantine", {"worker_id": worker_id}, **kwargs)

    def proxy_capabilities(self, **kwargs):
        return self.call("proxy_capabilities", **kwargs)

    def read_file(self, worker_id, path, **kwargs):
        r = self.call("read_file", {"worker_id": worker_id, "path": path}, **kwargs)
        return base64.b64decode(r["data"] or "", validate=True)

    def write_file(self, worker_id, path, data, **kwargs):
        if len(data) > 512 * 1024:
            raise APIError("invalid_argument")
        return self.call("write_file", {"worker_id": worker_id, "path": path, "data": base64.b64encode(data).decode()}, **kwargs)

    def list_files(self, worker_id, path=".", **kwargs):
        r = self.call("list_files", {"worker_id": worker_id, "path": path}, **kwargs)
        return json.loads(base64.b64decode(r["data"] or "", validate=True))

    def start_job(self, worker_id, argv, *, env=None, working_dir="", **kwargs):
        return self.call("start_job", {"worker_id": worker_id, "argv": argv, "env": env or [], "working_dir": working_dir}, **kwargs)

    def list_jobs(self, worker_id, **kwargs):
        return self.call("list_jobs", {"worker_id": worker_id}, **kwargs)

    def job(self, worker_id, job_id, **kwargs):
        return self.call("job", {"worker_id": worker_id, "job_id": job_id}, **kwargs)

    def job_logs(self, worker_id, job_id, offset=0, **kwargs):
        return self.call("job_logs", {"worker_id": worker_id, "job_id": job_id, "offset": offset}, **kwargs)

    def wait_job(self, worker_id, job_id, **kwargs):
        return self.call("wait_job", {"worker_id": worker_id, "job_id": job_id}, **kwargs)

    def kill_job(self, worker_id, job_id, **kwargs):
        return self.call("kill_job", {"worker_id": worker_id, "job_id": job_id}, **kwargs)

    def checkpoint(self, worker_id, options=None, **kwargs):
        return self.call("checkpoint", {"worker_id": worker_id, "options": options or {}}, **kwargs)

    def checkpoints(self, **kwargs):
        return self.call("checkpoints", {}, **kwargs)

    def inspect_checkpoint(self, checkpoint_id, **kwargs):
        return self.call("inspect_checkpoint", {"checkpoint_id": checkpoint_id, "options": {}}, **kwargs)

    def remove_checkpoint(self, checkpoint_id, **kwargs):
        return self.call("remove_checkpoint", {"checkpoint_id": checkpoint_id, "options": {}}, **kwargs)

    def restore_checkpoint(self, checkpoint_id, request, **kwargs):
        if request.get("Runtime") not in (None, "", "kata"):
            raise APIError("invalid_argument")
        info = self.inspect_checkpoint(checkpoint_id, **kwargs)
        request = dict(request)
        if request.get("Image") not in (None, "", info["image"]):
            raise APIError("invalid_argument")
        environment = dict(request.get("Environment") or {})
        if environment.get("image_id") not in (None, "", info["digest"]):
            raise APIError("invalid_argument")
        request["Image"] = info["image"]
        environment["image_id"] = info["digest"]
        request["Environment"] = environment
        return self.launch(request, **kwargs)

    def close(self):
        self._fail("transport_lost")
        with self._write_lock:
            try:
                self._process.stdin.close()
            except OSError:
                pass
        try:
            self._process.wait(timeout=12)
        except subprocess.TimeoutExpired:
            self._process.terminate()
            try:
                self._process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self._process.kill()
                self._process.wait(timeout=5)
        self._reader.join(timeout=2)
        self._process.stdout.close()

    def __enter__(self):
        return self

    def __exit__(self, *_):
        self.close()
