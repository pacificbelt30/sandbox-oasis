"""Real Kata SDK acceptance. Read trusted fixture configuration only from stdin."""
import json
import os
from pathlib import Path
import subprocess
import sys
import time

from oaiso import APIError, Client


def main():
    cfg = json.load(sys.stdin)
    req = cfg['request']
    options = cfg['options']
    binary = cfg['binary']
    expected_secrets = cfg['secrets']
    with Client(options, binary=binary) as c:
        assert c.capabilities['node']['ProtocolVersion'] == 2
        assert c.profiles() and c.resource_policies()
        w = c.create(req)
        assert c.create(req)['ID'] == w['ID']
        try:
            c.create({**req, 'ReadOnly': True})
            raise AssertionError('changed request was accepted')
        except APIError as e:
            assert e.code == 'conflict'
        assert c.request(req['RequestID'])['State'] == 'complete'
        c.start(w['ID'])
        assert c.get(w['ID'])['Status'] == 'running'
        result = c.exec(w['ID'], ['python3', '-c', 'import sys;sys.stdout.buffer.write(sys.stdin.buffer.read());sys.stderr.write("stderr-ok");sys.exit(7)'], stdin=b'\x00\xffsdk-python')
        assert result['exit_code'] == 7 and result['stdout'] == b'\x00\xffsdk-python' and result['stderr'] == b'stderr-ok'
        c.write_file(w['ID'], 'sdk-binary', b'\x00\xffworkspace')
        assert c.read_file(w['ID'], 'sdk-binary') == b'\x00\xffworkspace'
        assert 'sdk-binary' in c.list_files(w['ID'])
        job = c.start_job(w['ID'], ['python3', '-c', 'print("sdk-job-output");raise SystemExit(9)'])
        assert c.wait_job(w['ID'], job['id'])['exit_code'] == 9
        assert 'sdk-job-output' in c.job_logs(w['ID'], job['id'])['output']
        assert any(j['id'] == job['id'] for j in c.list_jobs(w['ID']))
        assert isinstance(c.checkpoints()['items'], list)
        usage = c.resource_usage(w['ID'])
        assert usage['Profile']['Name'] == 'compact' and usage['HostMemoryCurrentBytes'] > 0
        probe = c.exec(w['ID'], ['python3', '-c', 'import json,os;from pathlib import Path;p=Path.home();print(json.dumps(dict(os.environ)));[(print(f.read_text())) for d in (p/".codex",p/".claude") if d.is_dir() for f in d.rglob("*.json") if f.stat().st_size<100000]'])
        assert all(secret.encode() not in probe['stdout'] + probe['stderr'] for secret in expected_secrets), 'guest credential leak'
        waiting = c.submit('wait', {'worker_id': w['ID']})
        waiting.cancel().result(10)
        try:
            waiting.result(10)
            raise AssertionError('wait did not cancel')
        except APIError as e:
            assert e.code == 'canceled' and not e.requires_reconciliation
        assert c.get(w['ID'])['Status'] == 'running'
        heartbeat = Path(req['ProjectDir']) / 'python-heartbeat'
        pending = c.submit('exec', {'worker_id': w['ID'], 'argv': ['python3', '-c', 'import time;from pathlib import Path\np=Path("/workspace/python-heartbeat")\nwhile True: p.write_text(str(time.time_ns()));time.sleep(.05)']})
        end = time.monotonic() + 30
        while not heartbeat.exists():
            if pending._future.done():
                result = pending.result()
                raise AssertionError(f"guest exec ended before heartbeat: code={result['exit_code']} stderr={result['stderr']}")
            assert time.monotonic() < end, 'exec never started'
            time.sleep(.05)
        pending.cancel().result(10)
        try:
            pending.result(10)
            raise AssertionError('exec did not cancel')
        except APIError as e:
            assert e.code == 'canceled' and e.requires_reconciliation
        time.sleep(.2)
        before = heartbeat.read_text()
        time.sleep(.3)
        assert before == heartbeat.read_text(), 'canceled guest process still runs'

        # Lose the actual response to Create, then reconcile through a new call.
        lost = {**req, 'RequestID': cfg['lost_request_id']}
        child = subprocess.Popen([binary, 'api', 'stdio'], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
        try:
            child.stdin.write((json.dumps({'version': 1, 'id': 1, 'method': 'open', 'params': {'options': options}}) + '\n').encode()); child.stdin.flush()
            assert 'error' not in json.loads(child.stdout.readline())
            child.stdin.write((json.dumps({'version': 1, 'id': 2, 'method': 'create', 'params': {'request': lost}}) + '\n').encode()); child.stdin.flush()
            end = time.monotonic() + 60
            while True:
                try:
                    if c.request(lost['RequestID'])['State'] == 'complete': break
                except APIError as e:
                    assert e.code == 'not_found'
                assert time.monotonic() < end, 'lost request never completed'
                time.sleep(.1)
            # Do not read the Create response. Drop the transport instead.
            child.kill(); child.wait(timeout=5)
        finally:
            if child.poll() is None: child.kill(); child.wait(timeout=5)
            child.stdin.close(); child.stdout.close()
        recovered = c.create(lost)
        assert recovered['ID'] == c.request(lost['RequestID'])['WorkerID']
        c.remove_request(lost['RequestID'], True)
        c.remove_request(lost['RequestID'], True)
        c.stop(w['ID'], 0)
        assert c.get(w['ID'])['Status'] == 'exited'
        c.remove_request(req['RequestID'], True)
        c.remove_request(req['RequestID'], True)
        try:
            c.create(req)
            raise AssertionError('removed request was recreated')
        except APIError as e:
            assert e.code == 'request_gone'
    try:
        Client(options, binary=cfg['mismatched_binary'])
        raise AssertionError('wrong node version accepted')
    except APIError as e:
        assert e.code == 'version_mismatch', 'version mismatch category lost'
    print('PASS: Python real Kata lifecycle, replay, lost response, cancellation, secret exclusion, resource usage and cleanup')


if __name__ == '__main__':
    main()
