"""Drive managed current-file import and new-VM recovery using a real PTY."""
import fcntl
import json
import os
import pty
import select
import struct
import subprocess
import sys
import termios
import time

binary, project, name, config, mode = sys.argv[1:]
master, slave = pty.openpty()
previous = termios.tcgetattr(slave)
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 48, 150, 0, 0))
env = os.environ | {"TERM": "xterm-256color", "XDG_CONFIG_HOME": config}
process = subprocess.Popen([binary, "ui"], cwd=project, stdin=slave, stdout=slave, stderr=slave,
    env=env, start_new_session=True, preexec_fn=lambda: fcntl.ioctl(0, termios.TIOCSCTTY, 0))
screen, sent_at = Screen(), -1


def pump():
    ready, _, _ = select.select([master], [], [], .05)
    if ready:
        screen.feed(os.read(master, 65536))


def expect(marker, timeout=45):
    deadline = time.monotonic() + timeout
    while (marker not in screen.text() or screen.received <= sent_at) and time.monotonic() < deadline:
        pump()
        if process.poll() is not None:
            break
    assert marker in screen.text() and screen.received > sent_at, (marker, screen.text()[-7000:])
    return screen.text()


def send(value):
    global sent_at
    sent_at = screen.received
    os.write(master, value)


def paste(value):
    send(b"\x1b[200~" + value.encode() + b"\x1b[201~")


def catalog():
    out = subprocess.run([binary, "workspace", "list"], env=env, capture_output=True, timeout=15)
    assert out.returncode == 0, out.stderr
    return json.loads(out.stdout)


try:
    expect(b"sort=name")
    before = catalog()
    if mode == "save":
        send(b"/name:" + name.encode() + b"\r")
        expect(name.encode())
        expect(b"exited")
        send(b"k")
        expect(b"Save stopped VM")
        send(bytes([21]))
        paste("managed-recovery")
        send(b"\x1b[Z\x1b[Z\r")
        expect(b"Save recovery point")
        assert catalog() == before, "save review allocated a snapshot"
        send(b"\t\t\r")
        expect(b"Rootfs digest:", 180)
        send(bytes([27]))
        expect(b"Recovery points")
        send(bytes([27]))
        expect(b"sort=name")
        send(b"q")
        deadline = time.monotonic() + 10
        while process.poll() is None and time.monotonic() < deadline:
            pump()
        assert process.poll() == 0
        assert termios.tcgetattr(slave) == previous
        print("PASS: PTY stopped-VM save review, paired point creation, terminal restore")
        raise SystemExit(0)
    if mode == "create":
        send(b"n")
        expect(b"New sandbox")
        send(bytes([15]))
        expect(b"fixture")
        send(b"\r")
        expect(b"Load environment")
        send(b"\t\t\r")
        expect(b"New sandbox")
    else:
        send(b"z")
        expect(b"managed-recovery")
        send(b"\r")
        expect(b"Recovery point")
        send(b"\t\r")  # text -> first button: Restore as new VM
        expect(b"New sandbox")
    paste(name)
    send(bytes([18]))
    review = expect(b"Review sandbox launch")
    assert b"64 MiB / 1024 inodes" in review
    if mode == "create":
        assert b"dirty" not in review  # contents are represented by hashes, not printed.
        assert b"tracked.txt" in review and b"untracked.txt" in review and b"SKIP .env" in review
    else:
        assert b"NEW workspace" in review and b"Original workspace is unchanged" in review
    assert catalog() == before, "review created workspace resources"
    send(bytes([27]))
    expect(b"New sandbox")
    assert catalog() == before, "cancel created workspace resources"
    send(bytes([18]))
    expect(b"Review sandbox launch")
    send(b"\t\t\r")
    expect(b"Created sandbox", 120)
    send(b"\r")
    expect(b"New sandbox")
    send(bytes([27]))
    expect(b"sort=name")
    send(b"m")
    expect(b"MiB reserved")
    send(b"\r")
    expect(b"Physical capacity")
    send(bytes([27]))
    expect(b"MiB reserved")
    send(bytes([27]))
    expect(b"sort=name")
    send(b"q")
    deadline = time.monotonic() + 10
    while process.poll() is None and time.monotonic() < deadline:
        pump()
    assert process.poll() == 0
    assert termios.tcgetattr(slave) == previous
    print("PASS: PTY", mode, "review hashes/capacity, cancel without resources, managed catalog, new VM, terminal restore")
finally:
    if process.poll() is None:
        process.kill()
        process.wait()
    os.close(master)
    os.close(slave)
