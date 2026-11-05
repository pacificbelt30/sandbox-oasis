"""Exercise saved environments, independent copies, results and limits in Kata."""
import fcntl
import os
from pathlib import Path
import pty
import select
import struct
import subprocess
import sys
import termios
import time

binary, project, name, copy_dir, config_dir, archive = sys.argv[1:]
master, slave = pty.openpty()
previous = termios.tcgetattr(slave)
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 48, 150, 0, 0))
env = os.environ | {"TERM": "xterm-256color", "XDG_CONFIG_HOME": config_dir}
process = subprocess.Popen([binary, "ui"], cwd=project, stdin=slave, stdout=slave, stderr=slave,
    env=env, start_new_session=True, preexec_fn=lambda: fcntl.ioctl(0, termios.TIOCSCTTY, 0))
output = bytearray()




screen = Screen()
sent_at = -1


def pump():
    ready, _, _ = select.select([master], [], [], .05)
    if ready:
        data = os.read(master, 65536)
        output.extend(data)
        screen.feed(data)
        del output[:-131072]


def expect(marker, timeout=45):
    deadline = time.monotonic() + timeout
    while (marker not in screen.text() or screen.received <= sent_at) and time.monotonic() < deadline:
        pump()
        if process.poll() is not None:
            break
    assert marker in screen.text() and screen.received > sent_at, (marker, screen.text()[-7000:])
    result = screen.text()
    output.clear()
    return result


def send(value):
    global sent_at
    sent_at = screen.received
    os.write(master, value)


def paste(value):
    send(b"\x1b[200~" + value.encode() + b"\x1b[201~")


def reviewed_confirm():
    send(b"\t\t\r")  # scrollable text -> Cancel -> Confirm


try:
    expect(b"sort=name")
    send(b"n")
    expect(b"Esc cancels")
    send(bytes([15]))  # Ctrl-O: saved environments
    expect(b"fixture")
    send(b"\r")
    expect(b"Load environment")
    reviewed_confirm()
    expect(b"New sandbox")
    paste(name)
    send(b"\t" * 12)
    paste(copy_dir)
    send(bytes([18]))  # Ctrl-R: launch review
    review = expect(b"Independent copy")
    assert b"committed files" in review
    send(bytes([27]))
    expect(b"New sandbox")
    assert not Path(copy_dir).exists(), "cancel created the copy"
    send(bytes([18]))
    expect(b"Independent copy")
    reviewed_confirm()
    expect(b"Created", 90)
    send(b"\r")
    expect(b"New sandbox")
    send(bytes([27]))
    expect(b"sort=name")
    send(b"/name:" + name.encode() + b"\r")
    expect(name.encode())
    # Wait for selection from the asynchronous list refresh without fixed sleeps.
    deadline = time.monotonic() + 20
    while b"Lifetime / storage" not in output and time.monotonic() < deadline:
        send(b"l")
        until = time.monotonic() + .1
        while time.monotonic() < until:
            pump()
    expect(b"Lifetime / storage")
    limits = expect(b"Supervised by this TUI: false")
    assert b"Rootfs:" in limits and b"MiB" in limits
    send(b"\t\t\r")  # Refresh -> Supervise
    expect(b"already expired")
    send(b"\t\r")  # existing confirmation modal: No -> Yes
    expect(b"Supervised by this TUI: true")
    send(b"\t\t\r")  # stop this TUI's supervision
    expect(b"Supervised by this TUI: false")
    send(bytes([27]))
    expect(b"sort=name")
    send(b"x")
    expect(b"Command")
    send(b"git-receive-p")
    expect(b"git-receive-pack")  # Discovered inside the VM, not a builtin hint.
    send(b"\t")
    expect(b"git-receive-pack")
    send(bytes([27]))
    expect(b"sort=name")
    change = subprocess.run([binary, "exec", name, "--", "python3", "-I", "-c",
        "import os; assert open('/workspace/tracked.txt').read() == 'committed base\\n'; "
        "assert not os.path.exists('/workspace/untracked.txt'); "
        "open('/workspace/tracked.txt','w').write('guest change 日本語\\n'); "
        "open('/workspace/new.txt','w').write('new guest file\\n')"], env=env, capture_output=True, timeout=30)
    assert change.returncode == 0, change.stderr[-2000:]
    send(b"w")
    expect(b"Esc cancels this read")
    send(bytes([27]))
    expect(b"Use Refresh")
    send(b"\t\r")
    expect(b"guest change")
    send(b"\t\t\r")  # Refresh -> Export
    expect(b"Export selected workspace files")
    send(b"\t\t\t\r")  # paths -> destination -> Review -> Choose files
    expect(b"Choose artifact files")
    expect(b"new.txt")
    send(b" \x1b[B ")  # new.txt and tracked.txt; .git is excluded.
    send(b"\t\r")  # apply the draft selection without exporting
    expect(b"Export selected workspace files")
    assert not Path(archive).exists(), "picker exported before review approval"
    paste(archive)
    send(b"\t\r")
    expect(b"Review selected artifacts")
    reviewed_confirm()
    expect(b"Saved reviewed artifacts")
    assert Path(archive).is_file()
    send(b"\r")
    expect(b"Export selected workspace files")
    send(bytes([27]))
    expect(b"Workspace changes")
    send(bytes([27]))
    expect(b"sort=name")
    send(b"q")
    deadline = time.monotonic() + 10
    while process.poll() is None and time.monotonic() < deadline:
        pump()
    assert process.poll() == 0, output[-3500:]
    assert termios.tcgetattr(slave) == previous
    print("PASS: public TUI environment load/review; copy launch/cancel; limits/quota/scoped supervision; VM command completion without execution; cancelled guest read and refresh; artifact picker and reviewed export; VM retained after UI exit; terminal restore")
finally:
    if process.poll() is None:
        process.kill()
        process.wait()
    os.close(master)
    os.close(slave)
