"""Public TUI: create a no-AI Kata sandbox, transfer files and manage previews."""
import fcntl
import http.cookiejar
import json
import os
from pathlib import Path
import pty
import re
import select
import signal
import struct
import subprocess
import sys
import termios
import time
import urllib.error
import urllib.parse
import urllib.request

binary, project, image, name, source, download = sys.argv[1:]
project = Path(project)
expected = (project / "hello.txt").read_bytes()
master, slave = pty.openpty()
previous = termios.tcgetattr(slave)
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 48, 150, 0, 0))
process = subprocess.Popen([binary, "ui"], cwd=project, stdin=slave, stdout=slave, stderr=slave,
    env=os.environ | {"TERM": "xterm-256color"}, start_new_session=True,
    preexec_fn=lambda: fcntl.ioctl(0, termios.TIOCSCTTY, 0))
output = bytearray()


def pump():
    ready, _, _ = select.select([master], [], [], .05)
    if ready:
        output.extend(os.read(master, 65536))
        del output[:-131072]


def expect(marker, timeout=60):
    deadline = time.monotonic() + timeout
    while marker not in output and time.monotonic() < deadline:
        pump()
        if process.poll() is not None:
            break
    assert marker in output, (marker, output[-3500:])
    result = bytes(output)
    output.clear()
    return result


def send(value):
    os.write(master, value)


def paste(value):
    send(b"\x1b[200~" + value.encode() + b"\x1b[201~")


def approve():
    send(b"\t\r")


def new_preview():
    send(b"\t\t\r")  # port -> lifetime -> Start
    pattern = rb"http://127\.0\.0\.1:\d+/__oaiso_login\?token=[0-9a-f]{64}"
    deadline = time.monotonic() + 30
    match = re.search(pattern, output)
    while match is None and time.monotonic() < deadline:
        pump()
        match = re.search(pattern, output)
    assert match is not None, output[-3500:]
    url = match.group().decode()
    output.clear()
    return url


def browser(url):
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    origin = urllib.parse.urlsplit(url)
    base = f"{origin.scheme}://{origin.netloc}"
    deadline = time.monotonic() + 20
    while True:
        try:
            with opener.open(url, timeout=5) as response:
                response.read()
            with opener.open(base + "/hello.txt", timeout=5) as response:
                assert response.read() == expected
            return opener, base
        except (urllib.error.URLError, ConnectionError):
            if time.monotonic() >= deadline:
                raise
            pump()


def require_closed(opener, base):
    deadline = time.monotonic() + 15
    while time.monotonic() < deadline:
        try:
            with opener.open(base + "/hello.txt", timeout=2) as response:
                response.read()
        except (urllib.error.URLError, ConnectionError, TimeoutError):
            return
        pump()
    raise AssertionError("preview still accepts connections")


try:
    expect(b"sort=name")
    send(b"\x1bOQ")  # F2: discover actions and cancel without creating anything
    expect(b"New sandbox")
    send(bytes([27]))
    expect(b"sort=name")
    send(b" ")
    expect(b"New sandbox")
    send(b"\r")
    expect(b"Esc cancels")
    paste(name)
    send(b"\t" + bytes([21]))
    paste(str(project))
    send(b"\t")
    paste(image)
    send(b"\t")
    paste("exec python3 -u -m http.server 3000 --bind 127.0.0.1 --directory /workspace")
    send(b"\x1b[Z" * 5 + b"\r")
    expect(b"false")
    approve()
    expect(b"Created", 90)
    print("verified: sandbox created from the public TUI", flush=True)
    send(b"\r")
    expect(b"New sandbox")
    send(bytes([27]))
    expect(b"sort=name")
    send(b"/name:" + name.encode() + b"\r")
    # The new worker name appears only after the dashboard refresh observes it.
    expect(name.encode())
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 0, 0))
    os.kill(process.pid, signal.SIGWINCH)
    expect(b"STATUS")
    deadline = time.monotonic() + 30
    while b"captured" not in output and time.monotonic() < deadline:
        send(b"i")  # read-only: wait until asynchronous list refresh selects it
        until = time.monotonic() + .2
        while time.monotonic() < until:
            pump()
    details = expect(b"captured")
    assert name.encode() in details, "details lost the full sandbox name"
    send(bytes([27]))
    expect(b"sort=name")
    send(b" ")
    expect(b"Browse workspace files")
    send(b"F")
    expect(b"symlinks")
    send(b"\r")  # first sorted entry is an intentionally refused symlink
    expect(b"regular")
    send(b"\r")
    expect(b"Workspace files")
    send(b"\x1b[B\r")
    text = expect(b"[red]literal")
    assert b"HELLO_FROM_SANDBOX" in text
    send(bytes([27]))
    expect(b"Workspace files")
    send(b"d")
    expect(b"New host file")
    send(bytes([21]))
    paste(download)
    send(b"\t\r")
    expect(b"symlink parents")
    approve()
    expect(b"Downloaded")
    assert Path(download).read_bytes() == expected
    assert Path(download).stat().st_mode & 0o777 == 0o600
    print("verified: text viewer and private new-file download", flush=True)
    send(b"d")
    expect(b"New host file")
    send(bytes([21]))
    paste(download)
    send(b"\t\r")
    expect(b"symlink parents")
    approve()
    expect(b"requires")
    assert Path(download).read_bytes() == expected
    send(b"\r")
    expect(b"New host file")
    send(bytes([27]))
    expect(b"Workspace files")
    send(b"u")
    expect(b"Host source file")
    paste(source)
    send(b"\t")
    paste("uploaded.txt")
    send(b"\t\r")
    expect(b"SHA-256")
    send(bytes([27]))
    expect(b"Host source file")
    assert not (project / "uploaded.txt").exists()
    send(b"\r")
    expect(b"SHA-256")
    approve()
    expect(b"symlinks")  # wait for the refreshed file list, not echoed form input
    assert (project / "uploaded.txt").read_bytes() == Path(source).read_bytes()
    print("verified: upload review/cancel, frozen content and completed transfer", flush=True)
    send(bytes([27]))
    expect(b"sort=name")
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 48, 150, 0, 0))
    os.kill(process.pid, signal.SIGWINCH)
    expect(b"sort=name")
    send(b"\x1bOQ")
    expect(b"Web previews")
    send(b"u")
    expect(b"Guest loopback port")
    first = new_preview()
    opener, base = browser(first)
    plain = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    try:
        plain.open(base + "/hello.txt", timeout=5)
        raise AssertionError("unauthenticated preview accepted")
    except urllib.error.HTTPError as error:
        assert error.code in (401, 403)
    send(b"\x1b[Z\r")  # Shift-Tab: first field -> active URL list
    expect(b"connections")
    approve()
    expect(b"Active localhost previews")
    require_closed(opener, base)
    second = new_preview()
    opener, base = browser(second)
    send(bytes([27]))
    expect(b"sort=name")
    send(b"s")
    require_closed(opener, base)
    # Confirm the stop response using the public CLI before asking the TUI to
    # restart; keep consuming terminal output while either operation completes.
    deadline = time.monotonic() + 20
    while time.monotonic() < deadline:
        check = subprocess.run([binary, "exec", "--timeout", "2s", name, "--", "true"], capture_output=True)
        if check.returncode != 0:
            break
        pump()
    send(b"r")
    deadline = time.monotonic() + 60
    while True:
        check = subprocess.run([binary, "exec", "--timeout", "2s", name, "--", "true"], capture_output=True)
        if check.returncode == 0:
            break
        assert time.monotonic() < deadline, check.stderr[-2000:]
        pump()
    send(b"u")
    expect(b"Guest loopback port")
    third = new_preview()
    opener, base = browser(third)
    send(bytes([27]))
    expect(b"sort=name")
    send(b"q")
    deadline = time.monotonic() + 10
    while process.poll() is None and time.monotonic() < deadline:
        pump()
    assert process.poll() == 0, output[-3500:]
    require_closed(opener, base)
    assert termios.tcgetattr(slave) == previous
    print("PASS: public TUI actions menu/cancel, narrow-screen full details and no-AI Kata creation; safe file viewing/new transfers/cancel/no overwrite; authenticated preview/start/stop/worker-stop/restart/UI-exit; terminal restore")
finally:
    if process.poll() is None:
        process.kill()
        process.wait()
    os.close(master)
    os.close(slave)
