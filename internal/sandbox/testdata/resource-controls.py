"""Only synthetic traffic/files, in an explicitly disposable acceptance VM."""
import errno
import http.client
import json
import os
import resource
import socket
import time
import urllib.parse

assert resource.getrlimit(resource.RLIMIT_NOFILE) == (512, 512)
opened = []
try:
    for _ in range(1024):
        opened.append(os.open('/dev/null', os.O_RDONLY))
except OSError as error:
    assert error.errno == errno.EMFILE, error
else:
    raise AssertionError('file descriptor ceiling did not apply')
finally:
    for fd in opened:
        os.close(fd)
try:
    resource.setrlimit(resource.RLIMIT_NOFILE, (513, 513))
except (ValueError, PermissionError):
    pass
else:
    raise AssertionError('unprivileged code raised its hard ceiling')

egress = urllib.parse.urlsplit(os.environ['HTTP_PROXY'])
held = []
rejected = 0
try:
    for _ in range(12):
        connection = http.client.HTTPConnection(egress.hostname, egress.port, timeout=5)
        try:
            connection.request('GET', '/health')
            response = connection.getresponse()
            assert response.status == 200, response.status
            response.read()
            held.append(connection)
        except (ConnectionError, http.client.RemoteDisconnected):
            rejected += 1
            connection.close()
    assert 1 <= len(held) <= 8 and rejected > 0, (len(held), rejected)
    # New rejections do not terminate a connection that was already admitted.
    held[0].request('GET', '/health')
    assert held[0].getresponse().read(), 'existing connection interrupted'
finally:
    for connection in held:
        connection.close()

connection = http.client.HTTPConnection(egress.hostname, egress.port, timeout=20)
start = time.monotonic()
connection.request('GET', 'http://203.0.113.1/stream')
response = connection.getresponse()
assert response.status == 200
assert len(response.read(65536)) == 65536
elapsed = time.monotonic() - start
connection.close()
assert elapsed >= 0.25, ('bandwidth ceiling bypassed', elapsed)

# fsync exercises writeback; cached reads and tmpfs are deliberately not used
# to claim physical disk throughput. Host io.stat is checked by the controller.
start = time.monotonic()
path = '/workspace/resource-io-fixture'
with open(path, 'wb', buffering=0) as stream:
    for _ in range(16):
        stream.write(b'x' * (1 << 20))
    os.fsync(stream.fileno())
os.unlink(path)
print(json.dumps({'fd_opened': len(opened), 'accepted': len(held), 'rejected': rejected,
                  'transfer_seconds': elapsed, 'write_seconds': time.monotonic() - start}))
