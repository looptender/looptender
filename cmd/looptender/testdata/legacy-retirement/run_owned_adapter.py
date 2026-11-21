"""Explicitly released owned-child fixture only; never accepts a target PID.
Compile artifacts separately. Do not invoke without exact source/harness review.
"""
import argparse
import hashlib
import json
import os
import pathlib
import selectors
import stat
import subprocess
import time

from owned_process_evidence import run_logged

parser = argparse.ArgumentParser()
parser.add_argument('--cli', required=True)
parser.add_argument('--cli-sha256', required=True)
parser.add_argument('--child', required=True)
parser.add_argument('--child-sha256', required=True)
parser.add_argument('--evidence', required=True)
args = parser.parse_args()


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def artifact(name, expected):
    p = pathlib.Path(name)
    if not p.is_absolute() or p.resolve() != p:
        raise ValueError('artifact alias refused')
    s = p.lstat()
    if not stat.S_ISREG(s.st_mode) or s.st_uid != os.geteuid() or s.st_mode & 0o222:
        raise ValueError('artifact must be owned regular read-only')
    sha = digest(p.read_bytes())
    if sha != expected:
        raise ValueError('artifact hash mismatch')
    return dict(sha256=sha, device=s.st_dev, inode=s.st_ino,
                mode=stat.S_IMODE(s.st_mode), mtime_ns=s.st_mtime_ns)


pins = lambda: {'cli': artifact(args.cli, args.cli_sha256),
                'child': artifact(args.child, args.child_sha256)}
before = pins()
evidence = pathlib.Path(args.evidence)
if not evidence.is_absolute() or evidence.parent.resolve() != evidence.parent:
    raise ValueError('evidence parent alias refused')
# Dedicated new evidence directory; never overwrite a prior result or run root.
for binary in (pathlib.Path(args.cli), pathlib.Path(args.child)):
    if evidence == binary.parent or evidence in binary.parents or binary.parent in evidence.parents:
        raise ValueError('writable evidence and artifact parents overlap')
evidence.mkdir(mode=0o700)
report_path = evidence / 'result.json'
report_fd = os.open(report_path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
report = dict(artifacts_before=before, scenarios=[], validated=False,
              scope='two newly direct-owned fictional children; no external PID input')
started = time.monotonic()
deadline = started + 40


def save():
    raw = json.dumps(report, indent=2).encode()
    os.lseek(report_fd, 0, os.SEEK_SET)
    os.ftruncate(report_fd, 0)
    if os.write(report_fd, raw) != len(raw):
        raise OSError('short report write')
    os.fsync(report_fd)


def bound():
    left = deadline - time.monotonic()
    if left <= 0:
        raise TimeoutError('whole fixture deadline')
    return left


def read_ready(child):
    data = b''
    with selectors.DefaultSelector() as sel:
        sel.register(child.stdout, selectors.EVENT_READ)
        end = min(deadline, time.monotonic() + 4)
        while b'\n' not in data:
            left = end - time.monotonic()
            if left <= 0 or not sel.select(left):
                raise TimeoutError('owned child readiness')
            part = os.read(child.stdout.fileno(), 4097)
            if not part or len(data) + len(part) > 4096:
                raise ValueError('owned child readiness bound/EOF')
            data += part
    value = json.loads(data)
    if set(value) != {'pid', 'startedAt'} or value['pid'] != child.pid:
        raise ValueError('readiness does not match direct owned child')
    return value


def invoke(command, env, cwd, entries):
    def check():
        if pins() != before:
            raise ValueError('artifact drift')
    return run_logged(command, env, cwd, entries, save, check, deadline)


def control_snapshot(path):
    s = path.lstat()
    return dict(hash=digest(path.read_bytes()), device=s.st_dev, inode=s.st_ino,
                mode=stat.S_IMODE(s.st_mode), mtime_ns=s.st_mtime_ns)


try:
    save()
    for mode in ('graceful', 'ignore'):
        bound()
        root = evidence / mode
        root.mkdir(mode=0o700)
        state = root / 'state'
        state.mkdir(mode=0o700)
        env = {'PATH': '/usr/bin:/bin', 'LANG': 'C', 'LC_ALL': 'C',
               'BB_THREAD_ID': 'thr_example', 'OWNED_RETIRE_FIXTURE_ROOT': str(root),
               'OWNED_RETIRE_FIXTURE_MODE': mode}
        for key in ('HOME', 'TMPDIR', 'XDG_CONFIG_HOME', 'XDG_STATE_HOME',
                    'LOOPTENDER_SESSION_DIR', 'KEEPGOING_SESSION_DIR',
                    'LOOPTENDER_REGISTRY_DIR', 'KEEPGOING_REGISTRY_DIR',
                    'LOOPTENDER_ROUTES_FILE', 'KEEPGOING_ROUTES_FILE',
                    'CLAUDE_CONFIG_DIR', 'CODEX_HOME'):
            p = root / key
            if not key.endswith('ROUTES_FILE'):
                p.mkdir(mode=0o700)
            env[key] = str(p)
        config = root / 'loop.yaml'
        config.write_text('version: 1\nloop:\n  name: example-loop\n  state_dir: state\n'
                          '  adapter: bb\n  provider: codex\n'
                          '  bb: {thread: thr_example, cli: /fictional/absent-bb}\n'
                          '  prompt: Continue the fictional loop.\n')
        config.chmod(0o600)
        controls = state / 'control.json'
        controls.write_text('{"enabled":true,"paused":true,"stop":true,"awaitingUser":true,'
                            '"completed":true,"ownership":{"threadId":"thr_example","generation":2}}')
        for name, raw in [('state.json', '{"activeTarget":"primary"}'),
                          ('tick.lock', ''), ('control.lock', ''), ('daemon.lock', '')]:
            (state / name).write_text(raw)
        for p in state.iterdir():
            p.chmod(0o600)
        baseline = control_snapshot(controls)
        row = dict(mode=mode, environment=env, commands=[], control_before=baseline)
        report['scenarios'].append(row)
        child = None
        scenario_error = None
        try:
            if pins() != before:
                raise ValueError('artifact drift before child')
            row['child_state'] = 'started'
            save()
            child = subprocess.Popen([args.child, 'daemon', '-c', str(config)],
                                     env=env, cwd=root, stdin=subprocess.PIPE,
                                     stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            row['owned_child_pid'] = child.pid
            save()
            # No poll/wait/reap until all CLI operations on this child are over.
            # Its PID stays reserved even after graceful exit.
            ready = read_ready(child)
            row['owned_child_pid'] = child.pid
            selection = dict(configPath=str(config), statePath=str(state), pid=child.pid,
                             build='fictional-retirement-fixture', startedAt=ready['startedAt'],
                             executable=args.child, executableDigest=args.child_sha256,
                             arguments=[args.child, 'daemon', '-c', str(config)])
            selection_path = root / 'selection.json'
            selection_path.write_text(json.dumps(selection))
            selection_path.chmod(0o600)
            ticket = root / 'ticket.json'

            def call(action, ticket_path=ticket):
                command = [args.cli, 'legacy-retire', action, '--ticket', str(ticket_path), '--timeout', '1s']
                if action != 'reconcile':
                    command += ['--selection', str(selection_path)]
                result = invoke(command, env, root, row['commands'])
                return result

            prepared = call('prepare')
            if prepared['exit'] != 0 or prepared['stderr']:
                raise ValueError('prepare refused')
            executed = call('execute')
            if (executed['exit'] == 0) != (mode == 'graceful') or (mode == 'graceful' and executed['stderr']):
                raise ValueError('unexpected execution outcome')
            observed = call('reconcile')
            expected = ('recorded-completion-not-launch-authority' if mode == 'graceful' else 'consumed-unknown')
            if observed['exit'] != 0 or observed['stderr'] or not observed['stdout'].startswith(expected + '\n'):
                raise ValueError('unexpected reconciliation')
            if call('execute')['exit'] == 0:
                raise ValueError('spent replay accepted')
            if mode == 'ignore' and call('prepare', root / 'second-ticket.json')['exit'] == 0:
                raise ValueError('unknown attempt replaced')
            if (root / 'signals').read_text() != '1\n':
                raise ValueError('not exactly one handled request')
            row['control_after'] = control_snapshot(controls)
            if row['control_after'] != baseline:
                raise ValueError('controls changed')
            row['validated'] = True
        except BaseException as exc:
            scenario_error = exc
            row['failure'] = type(exc).__name__ + ': ' + str(exc)
            row['validated'] = False
        finally:
            if child is not None:
                # First release retained pipe; finite child timer bounds failure.
                # Never signal this target for cleanup or use PID polling.
                child.stdin.close()
                try:
                    row['child_exit'] = child.wait(timeout=13)
                    row['child_stdout_tail'] = child.stdout.read(4097).decode(errors='replace')
                    row['child_stderr'] = child.stderr.read(4097).decode(errors='replace')
                    if row['child_exit'] != 0 or row['child_stdout_tail'] or row['child_stderr']:
                        row['validated'] = False
                        raise ValueError('owned child cleanup/early-exit diagnostic')
                except BaseException as cleanup_error:
                    row['cleanup_error'] = type(cleanup_error).__name__ + ': ' + str(cleanup_error)
                    row['validated'] = False
                    if scenario_error is None:
                        scenario_error = cleanup_error
                finally:
                    child.stdout.close()
                    child.stderr.close()
            save()
        if scenario_error is not None:
            raise scenario_error
        if not row.get('validated'):
            raise ValueError('scenario incomplete')
    report['artifacts_after'] = pins()
    report['validated'] = report['artifacts_after'] == before
except BaseException as exc:
    report['failure'] = type(exc).__name__ + ': ' + str(exc)
    report['validated'] = False
finally:
    report['elapsed_seconds'] = time.monotonic() - started
    save()
    os.close(report_fd)
    fd = os.open(evidence, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)
print(json.dumps({'validated': report['validated'], 'evidence': str(report_path)}))
raise SystemExit(0 if report['validated'] else 1)
