"""Offline SQLite durability/quota probe inside an owned, verified Kata VM."""
import ctypes
import errno
import os
import pathlib
import sqlite3
import subprocess
import sys
import time

assert ctypes.CDLL(None).prctl(38, 1, 0, 0, 0) == 0
root = pathlib.Path(os.environ['CODEX_SQLITE_HOME'])
assert str(root) == '/var/lib/oaiso-agent-state/codex', root
root.mkdir(mode=0o700, parents=True, exist_ok=True)
assert root.parent.stat().st_uid == os.getuid()
assert root.parent.stat().st_mode & 0o777 == 0o700
mode, token = sys.argv[1:]
database = root / 'acceptance.sqlite'

if mode == 'wal':
    ready = pathlib.Path('/workspace/wal-ready')
    ready.unlink(missing_ok=True)
    child = '''import os,pathlib,sqlite3,sys,time
root=pathlib.Path(os.environ['CODEX_SQLITE_HOME'])
db=sqlite3.connect(root/'acceptance.sqlite')
db.execute('PRAGMA synchronous=FULL')
db.execute('PRAGMA wal_autocheckpoint=0')
db.execute('INSERT INTO crash_log VALUES (?)',(sys.argv[1],))
db.commit()
assert (root/'acceptance.sqlite-wal').stat().st_size>0
pathlib.Path('/workspace/wal-ready').write_text('committed')
time.sleep(600)
'''
    subprocess.Popen([sys.executable, '-c', child, token], stdin=subprocess.DEVNULL,
                     stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True)
    deadline = time.monotonic() + 10
    while not ready.exists():
        assert time.monotonic() < deadline, 'WAL holder did not commit'
        time.sleep(.05)
    print('SQLITE_DURABLE_wal', flush=True)
    sys.exit(0)

with sqlite3.connect(database) as db:
    assert db.execute('PRAGMA journal_mode=WAL').fetchone()[0] == 'wal'
    db.execute('PRAGMA synchronous=FULL')
    if mode == 'seed':
        db.execute('CREATE TABLE durable (value TEXT)')
        db.execute('CREATE TABLE crash_log (value TEXT)')
        db.execute('CREATE TABLE quota (value BLOB)')
        db.execute('INSERT INTO durable VALUES (?)', (token,))
    else:
        assert db.execute('SELECT value FROM durable').fetchall() == [(token,)]
        if mode in ('crash-verify', 'quota'):
            assert db.execute('SELECT value FROM crash_log').fetchall() == [(token,)]
        if mode == 'quota':
            filler = root / 'owned-quota-fixture'
            fd = os.open(filler, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
            allocated = 0
            full = False
            try:
                for chunk in (1 << 20, 4096):
                    while allocated < 96 << 20:
                        try:
                            os.posix_fallocate(fd, allocated, chunk)
                            allocated += chunk
                        except OSError as error:
                            assert error.errno == errno.ENOSPC, error
                            full = True
                            break
                os.fsync(fd)
                assert full and allocated < 96 << 20, 'disk capacity was not bounded'
                try:
                    db.execute('INSERT INTO quota VALUES (zeroblob(4194304))')
                    db.commit()
                except sqlite3.OperationalError as error:
                    assert error.sqlite_errorcode == sqlite3.SQLITE_FULL, error
                    db.rollback()
                else:
                    raise AssertionError('SQLite unexpectedly wrote beyond the full filesystem')
            finally:
                os.close(fd)
                filler.unlink()
            db.execute('INSERT INTO quota VALUES (?)', (b'recovered',))
            db.commit()
            assert db.execute('SELECT value FROM quota').fetchall() == [(b'recovered',)]
    assert db.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
print('SQLITE_DURABLE_' + mode, flush=True)
