"""Runs only in the opt-in Kata acceptance VM; uses synthetic upstreams."""
import http.client
import json
import os
import pathlib
import socket
import time
import urllib.parse

egress = urllib.parse.urlsplit(os.environ['HTTP_PROXY'])
# The entrypoint removes bootstrap-only variables before starting user code.
# These are the normal client endpoint and short-lived proxy placeholder.
auth = urllib.parse.urlsplit(os.environ['OPENAI_BASE_URL'])
token = os.environ['OPENAI_API_KEY']
held = []

def connect(authority, expected):
    conn = socket.create_connection((egress.hostname, egress.port), timeout=5)
    conn.sendall(f'CONNECT {authority} HTTP/1.1\r\nHost: {authority}\r\n\r\n'.encode())
    response = http.client.HTTPResponse(conn)
    response.begin()
    assert response.status == expected, (authority, response.status)
    if expected != 200:
        response.close()
        conn.close()
        return
    conn.sendall(b'tunnel-ready')
    assert conn.recv(12) == b'tunnel-ready'
    held.extend([conn, response])

for authority in ['127.0.0.1:443', '169.254.169.254:443', '100.100.100.200:443',
                  '[::1]:443', '[fd00:ec2::254]:443', '[fe80::1]:443',
                  '[::ffff:169.254.169.254]:443', '203.0.113.1:22']:
    connect(authority, 403)
    conn = http.client.HTTPConnection(egress.hostname, egress.port, timeout=5)
    conn.request('GET', f'http://{authority}/')
    response = conn.getresponse()
    assert response.status == 403, (authority, response.status)
    response.read()
    conn.close()

conn = http.client.HTTPConnection(egress.hostname, egress.port, timeout=5)
conn.request('GET', 'http://203.0.113.1/redirect-metadata')
response = conn.getresponse()
assert response.status == 403, ('redirect', response.status)
response.read()
conn.close()

connect('203.0.113.1:443', 200)
connect('[2606:4700:4700::1111]:443', 200)
conn = http.client.HTTPConnection(egress.hostname, egress.port, timeout=120)
conn.request('GET', 'http://203.0.113.1/stream')
response = conn.getresponse()
assert response.status == 200 and response.readline() == b'ready\n'
held.extend([conn, response])

conn = http.client.HTTPConnection(auth.hostname, auth.port, timeout=120)
conn.request('POST', '/v1/responses', body='{"stream":true}',
             headers={'Authorization': 'Bearer ' + token})
response = conn.getresponse()
assert response.status == 200 and response.readline().startswith(b'data: ')
held.extend([conn, response])

conn = socket.create_connection((auth.hostname, auth.port), timeout=5)
conn.sendall(('GET /v1/responses HTTP/1.1\r\nHost: proxy\r\nUpgrade: websocket\r\n'
              'Connection: Upgrade\r\nAuthorization: Bearer ' + token + '\r\n\r\n').encode())
response = http.client.HTTPResponse(conn)
response.begin()
assert response.status == 101
conn.sendall(b'ws-ready')
assert conn.recv(8) == b'ws-ready'
held.extend([conn, response])
pathlib.Path('/workspace/connections-ready').write_text(json.dumps({'egress': 3, 'auth': 2}))
while True:
    time.sleep(1)
