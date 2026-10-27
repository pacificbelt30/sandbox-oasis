"""Check public dashboard -> shell -> dashboard -> policies -> dashboard."""
import fcntl
import os
import pty
import select
import signal
import struct
import subprocess
import sys
import termios
import time

master, slave = pty.openpty()
previous = termios.tcgetattr(slave)
fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 35, 130, 0, 0))
environment = os.environ | {"TERM": "xterm-256color"}
process = subprocess.Popen([sys.argv[1], "ui"], stdin=slave, stdout=slave, stderr=slave, env=environment,
                           start_new_session=True, preexec_fn=lambda: fcntl.ioctl(0, termios.TIOCSCTTY, 0))
output = bytearray()


def wait_for(marker, timeout=20):
    output.clear()
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        ready, _, _ = select.select([master], [], [], .1)
        if ready:
            try:
                output.extend(os.read(master, 65536))
            except OSError:
                break
        if marker in output:
            return
        if process.poll() is not None:
            break
    raise RuntimeError("TUI did not respond: " + repr(marker) + " " + repr(output[-2000:]))


try:
    wait_for(b"sort=name")
    os.write(master, b"/id:" + sys.argv[2].encode() + b"\r")
    # Let the UI process the filter before selecting the worker. This is a
    # rendering synchronization, never used to infer guest raw-mode readiness.
    time.sleep(.2)
    os.write(master, b"c")
    wait_for(b"$ ")
    os.write(master, b"printf 'TUI_SHELL_READY\\n'\r")
    wait_for(b"\r\nTUI_SHELL_READY\r\n")
    os.write(master, b"exit\r")
    wait_for(b"sort=name")
    os.write(master, b"g")
    wait_for(b"Ctrl-S")
    fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 0, 0))
    os.kill(process.pid, signal.SIGWINCH)
    os.write(master, bytes([27]))
    wait_for(b"sort=name")
    os.write(master, b"q")
    process.wait(timeout=5)
    assert process.returncode == 0
    assert termios.tcgetattr(slave) == previous, "TUI did not restore terminal flags"
    print("PASS: TUI filter -> connect -> shell exit -> responsive dashboard -> policies -> resize/Esc -> quit")
finally:
    if process.poll() is None:
        process.kill()
        process.wait()
    os.close(master)
    os.close(slave)
