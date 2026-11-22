"""Bounded evidence for a directly spawned fixture command, never a target PID."""
import os
import selectors
import subprocess
import time


def run_logged(command, env, cwd, entries, save, check, deadline, timeout=5, cap=4096):
    row = dict(command=list(command), state='started', pid=None, exit=None,
               stdout='', stderr='', timeout=False, truncated=False,
               cleanup='not-spawned', error=None)
    entries.append(row)
    # A durable intent must exist before pin checks or Popen. If saving fails,
    # propagate immediately without launching anything.
    save()
    started = time.monotonic()
    p = None
    output = {'stdout': b'', 'stderr': b''}
    original = None
    end = min(deadline, started + timeout)
    try:
        if end <= time.monotonic():
            raise TimeoutError('command deadline before spawn')
        check()
        p = subprocess.Popen(command, env=env, cwd=cwd, stdin=subprocess.DEVNULL,
                             stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        row.update(pid=p.pid, cleanup='owned-child-pending')
        with selectors.DefaultSelector() as sel:
            sel.register(p.stdout, selectors.EVENT_READ, 'stdout')
            sel.register(p.stderr, selectors.EVENT_READ, 'stderr')
            while sel.get_map():
                left = end - time.monotonic()
                if left <= 0:
                    raise TimeoutError('command deadline')
                for key, _ in sel.select(left):
                    part = os.read(key.fileobj.fileno(), cap + 1)
                    if not part:
                        sel.unregister(key.fileobj)
                        continue
                    combined = output[key.data] + part
                    output[key.data] = combined[:cap]
                    if len(combined) > cap:
                        row['truncated'] = True
                        raise ValueError('command output bound')
        p.wait(timeout=max(0.001, end - time.monotonic()))
        row['cleanup'] = 'reaped'
    except BaseException as exc:
        original = exc
        row['error'] = type(exc).__name__ + ': ' + str(exc)
        row['timeout'] = isinstance(exc, (TimeoutError, subprocess.TimeoutExpired))
    finally:
        if p is not None:
            try:
                if p.returncode is None:
                    # Only this unreaped directly spawned command, never its
                    # selected target or process group. No PID input accepted.
                    p.kill()
                    p.wait(timeout=1)
                    row['cleanup'] = 'owned-command-killed-and-reaped'
            except BaseException as cleanup_error:
                row['cleanup'] = 'unknown'
                row['cleanup_error'] = type(cleanup_error).__name__ + ': ' + str(cleanup_error)
                if original is None:
                    original = cleanup_error
            finally:
                row['exit'] = p.returncode
                for stream in (p.stdout, p.stderr):
                    try:
                        stream.close()
                    except BaseException as close_error:
                        row['close_error'] = type(close_error).__name__ + ': ' + str(close_error)
                        if original is None:
                            original = close_error
        row.update(stdout=output['stdout'].decode(errors='replace'),
                   stderr=output['stderr'].decode(errors='replace'),
                   elapsed_seconds=time.monotonic()-started,
                   state='failed' if original is not None else 'finished')
        try:
            save()
        except BaseException as save_error:
            # Never replace a timeout/read/spawn failure with a later save error.
            row['evidence_error'] = type(save_error).__name__ + ': ' + str(save_error)
            if original is None:
                original = save_error
    if original is not None:
        raise original
    return row
