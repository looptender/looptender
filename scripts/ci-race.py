#!/usr/bin/env python3
"""Run the native race suite, with two disjoint command partitions per runner."""
import collections
import datetime
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import tempfile
import time

MODULE = 'github.com/looptender/looptender/'
COMMAND = MODULE + 'cmd/looptender'
# The command package runs as PARTITIONS jobs, one per runner, each with its
# own BUDGET seconds of execution. scripts/ci-workflow-test.py fails if the
# workflow's matrix does not list exactly these indexes.
PARTITIONS = 3
BUDGET = 600
WEIGHTS_FILE = Path(__file__).with_name('ci-race-weights.json')


def inventory(output):
    names = []
    for name in output.splitlines():
        if re.fullmatch(r'(Test|Example|Fuzz)\w*', name):
            names.append(name)
        elif not re.fullmatch(r'Benchmark\w+', name):
            raise ValueError(f'unexpected test inventory line: {name!r}')
    if not names or len(names) != len(set(names)):
        raise ValueError('empty or duplicate test inventory')
    return names


def validate_partition(names, groups):
    if collections.Counter(names) != collections.Counter(n for g in groups for n in g):
        raise ValueError('partition omits or duplicates inventory entries')
    if any(not g for g in groups):
        raise ValueError('empty partition')


def load_weights(path=WEIGHTS_FILE):
    data = json.loads(Path(path).read_text())
    return data['default'], data['tests']


def partition(names, count=PARTITIONS, weights=None):
    """Split names into count groups, balanced by weight, deterministically.

    Weights only decide balance. Every name lands in exactly one group, and
    validate_partition proves it before anything runs.
    """
    default, known = weights if weights is not None else load_weights()
    groups = [[] for _ in range(count)]
    loads = [0.0] * count
    for name in sorted(names, key=lambda n: (-known.get(n, default), n)):
        lightest = min(range(count), key=lambda i: (loads[i], i))
        groups[lightest].append(name)
        loads[lightest] += known.get(name, default)
    groups = [sorted(g) for g in groups]
    validate_partition(names, groups)
    return groups


def package_groups(packages, command_loads, count=PARTITIONS, weights=None):
    """Spread the packages other than the command package over the legs.

    Each goes to the leg with the least work so far, counting its command
    share. Every package lands in exactly one group.
    """
    if weights is None:
        data = json.loads(WEIGHTS_FILE.read_text())
        weights = (data['default_package'], data['packages'])
    default, known = weights
    groups = [[] for _ in range(count)]
    loads = list(command_loads)
    for name in sorted(packages, key=lambda p: (-known.get(p.removeprefix(MODULE), default), p)):
        lightest = min(range(count), key=lambda i: (loads[i], i))
        groups[lightest].append(name)
        loads[lightest] += known.get(name.removeprefix(MODULE), default)
    if sorted(p for g in groups for p in g) != sorted(packages):
        raise ValueError('package split omits or duplicates a package')
    return groups


def read_events(path):
    events = []
    with open(path) as stream:
        for line in stream:
            try:
                events.append(json.loads(line))
            except ValueError:
                pass  # a line cut short by the stop
    return events


def seconds_between(start, end):
    def parse(text):
        text = re.sub(r'(\.\d{6})\d*', r'\1', text.replace('Z', '+00:00'))
        return datetime.datetime.fromisoformat(text)
    return (parse(end) - parse(start)).total_seconds()


def timings(path):
    """Finished top-level tests with their seconds, and those still running."""
    finished, running, last = {}, {}, None
    for event in read_events(path):
        last = event.get('Time', last)
        name = event.get('Test', '')
        if not name or '/' in name:
            continue
        if event['Action'] == 'run':
            running[name] = event.get('Time')
        elif event['Action'] in ('pass', 'fail', 'skip'):
            running.pop(name, None)
            finished[name] = event.get('Elapsed', 0.0)
    open_for = {n: seconds_between(t, last) for n, t in running.items() if t and last}
    return finished, open_for


def describe(path, index, count, budget, elapsed):
    finished, open_for = timings(path)
    slowest = sorted(finished.items(), key=lambda kv: -kv[1])[:5]
    text = (f'command partition {index + 1} of {count}: {elapsed:.0f}s of its {budget}s budget '
            f'({100 * elapsed / budget:.0f}%); {len(finished)} tests finished')
    if open_for:
        text += '; still running: ' + ', '.join(
            f'{n} ({s:.0f}s so far)' for n, s in sorted(open_for.items(), key=lambda kv: -kv[1])[:5])
    if slowest:
        text += '; slowest finished: ' + ', '.join(f'{n} {s:.1f}s' for n, s in slowest)
    return text


def expression(names):
    return '^(' + '|'.join(re.escape(n) for n in names) + ')$'


def verify_execution(path, names):
    started, ended = [], []
    with open(path) as stream:
        for line in stream:
            event = json.loads(line)
            name = event.get('Test', '')
            if not name or '/' in name:
                continue
            if event['Action'] == 'run':
                started.append(name)
            if event['Action'] in ('pass', 'skip', 'fail'):
                ended.append(name)
    expected = collections.Counter(names)
    if collections.Counter(started) != expected or collections.Counter(ended) != expected:
        raise ValueError(f'{path}: executed tests differ from selected inventory')


class Job:
    def __init__(self, argv, cwd, log, names=None):
        self.log, self.names = log, names
        self.started = time.monotonic()
        self.stopped = False
        self.output = open(log, 'w')
        try:
            self.process = subprocess.Popen(argv, cwd=cwd, stdout=self.output,
                                            stderr=subprocess.STDOUT, start_new_session=True)
        except BaseException:
            self.output.close()
            raise

    def finish(self):
        code = self.process.wait()
        self.output.close()
        seconds = round(time.monotonic() - self.started, 2)
        print(f'{self.log.name}: {seconds}s, exit {code}', flush=True)
        if code:
            raise RuntimeError(f'{self.log.name} failed ({code})')
        if self.names is not None:
            verify_execution(self.log, self.names)

    def stop(self, grace=5):
        if self.stopped:
            return
        # The leader can exit while a descendant still owns this process group.
        # Check the group itself, never a PID supplied by a test fixture.
        def group_alive():
            # Reap a dead leader before probing: Darwin can return EPERM for
            # a process group containing only that unreaped zombie.
            self.process.poll()
            try:
                os.killpg(self.process.pid, 0)
                return True
            except ProcessLookupError:
                return False

        # Defer cancellation until this owned group is gone. A repeated signal
        # must not mark incomplete cleanup done or strand a sibling process.
        old_mask = signal.pthread_sigmask(signal.SIG_BLOCK, {signal.SIGINT, signal.SIGTERM})
        try:
            if group_alive():
                try:
                    os.killpg(self.process.pid, signal.SIGTERM)
                except ProcessLookupError:
                    pass
                until = time.monotonic() + grace
                while group_alive() and time.monotonic() < until:
                    self.process.poll()  # reap the leader, independently of descendants
                    time.sleep(0.02)
                if group_alive():
                    try:
                        os.killpg(self.process.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
            self.process.wait()
            self.stopped = True
        finally:
            self.output.close()
            signal.pthread_sigmask(signal.SIG_SETMASK, old_mask)


def wait_jobs(jobs, deadline, overrun=None):
    pending = list(jobs)
    errors = []
    while pending:
        for job in pending[:]:
            if job.process.poll() is not None:
                pending.remove(job)
                try:
                    job.finish()
                except Exception as error:
                    errors.append(str(error))
                finally:
                    # Release ownership now; a later idempotent cleanup must not
                    # signal a recycled group identifier from an earlier guard.
                    job.stop()
        # Only a command partition has a budget; other packages keep go test's
        # own default per-package timeout.
        if any(j.names is not None for j in pending) and time.monotonic() >= deadline:
            raise TimeoutError(overrun() if overrun else 'command partition exhausted its budget')
        if pending:
            time.sleep(0.1)
    if errors:
        raise RuntimeError('; '.join(errors))


def cleanup_jobs(jobs):
    # The first cancellation already failed the run. Ignore repeats while
    # cleaning every sibling and publishing all available logs.
    previous = {sig: signal.signal(sig, signal.SIG_IGN)
                for sig in (signal.SIGINT, signal.SIGTERM)}
    errors = []
    try:
        for job in jobs:
            try:
                job.stop()
            except Exception as error:
                errors.append(f'{job.log.name}: {error}')
        for job in jobs:
            try:
                print(f'::group::{job.log.name}', flush=True)
                print(job.log.read_text(), flush=True)
                print('::endgroup::', flush=True)
            except Exception as error:
                errors.append(f'{job.log.name} log: {error}')
    finally:
        for sig, handler in previous.items():
            signal.signal(sig, handler)
    if errors:
        raise RuntimeError('; '.join(errors))


def summary(text):
    print(text, flush=True)
    path = os.environ.get('GITHUB_STEP_SUMMARY')
    if path:
        with open(path, 'a') as stream:
            stream.write(text + '\n\n')


def output_dir():
    # Retain the inventory and complete logs, including on cancellation or failure.
    output = Path(os.environ.get('RUNNER_TEMP', tempfile.gettempdir())) / 'ci-race'
    output.mkdir(exist_ok=True)
    return output


def run_guarded(jobs, wait):
    def interrupted(signum, frame):
        raise InterruptedError(f'received signal {signum}')

    previous = {sig: signal.signal(sig, interrupted) for sig in (signal.SIGINT, signal.SIGTERM)}
    try:
        wait()
    finally:
        try:
            cleanup_jobs(jobs)
        finally:
            for sig, handler in previous.items():
                signal.signal(sig, handler)


def run_command_partition(index):
    if not 0 <= index < PARTITIONS:
        raise ValueError(f'partition {index} is outside 0..{PARTITIONS - 1}')
    root = Path(__file__).resolve().parents[1]
    output = output_dir()
    with tempfile.TemporaryDirectory(prefix='ci-race-build-') as build:
        binary = str(Path(build) / 'command.test')
        subprocess.run(['go', 'test', '-race', '-c', '-o', binary, './cmd/looptender'],
                       cwd=root, check=True)
        listing = subprocess.check_output([binary, '-test.list=.'],
                                          cwd=root / 'cmd/looptender', text=True)
        names = inventory(listing)
        # Every leg computes the whole plan and proves it covers the inventory
        # exactly once before running its own share of it.
        groups = partition(names)
        (output / f'inventory-{index}.json').write_text(json.dumps(groups, indent=2) + '\n')
        mine = groups[index]
        log = output / f'command-{index}.jsonl'
        started = time.monotonic()
        deadline = started + BUDGET
        # go's own timeout is a little later, so the message below wins the race.
        argv = ['go', 'tool', 'test2json', '-t', '-p', COMMAND, binary,
                '-test.v=test2json', '-test.count=1', f'-test.timeout={BUDGET + 30}s',
                '-test.run=' + expression(mine)]
        jobs = [Job(argv, root / 'cmd/looptender', log, mine)]

        def overrun():
            return describe(log, index, PARTITIONS, BUDGET, time.monotonic() - started)

        try:
            run_guarded(jobs, lambda: wait_jobs(jobs, deadline, overrun))
        except TimeoutError as error:
            summary(f'**Over budget: {error}**')
            raise
        except Exception:
            if log.exists():
                summary(f'**Failed: {describe(log, index, PARTITIONS, BUDGET, time.monotonic() - started)}**')
            raise
        text = describe(log, index, PARTITIONS, BUDGET, time.monotonic() - started)
        summary(f'Command partition ran {len(mine)} of {len(names)} tests: {text}')
        print('Native race inventory for this partition executed exactly once.', flush=True)
        default, known = load_weights()
        loads = [sum(known.get(n, default) for n in g) for g in groups]
        run_packages(root, output, index, loads)


def run_packages(root, output, index, loads):
    packages = subprocess.check_output(['go', 'list', './...'], cwd=root, text=True).splitlines()
    if packages.count(COMMAND) != 1:
        raise ValueError('command package missing or duplicated')
    mine = package_groups([p for p in packages if p != COMMAND], loads)[index]
    if not mine:
        return
    started = time.monotonic()
    jobs = [Job(['go', 'test', '-race', *mine], root, output / f'packages-{index}.log')]
    run_guarded(jobs, lambda: wait_jobs(jobs, float('inf')))
    summary(f'Packages in this leg: {", ".join(p.removeprefix(MODULE) for p in mine)}; '
            f'{time.monotonic() - started:.0f}s.')


def weights(paths):
    seen = collections.defaultdict(list)
    for path in paths:
        finished, _ = timings(path)
        for name, seconds in finished.items():
            seen[name].append(seconds)
    data = json.loads(WEIGHTS_FILE.read_text())
    heavy = {n: round(sorted(v)[len(v) // 2], 1) for n, v in seen.items()
             if sorted(v)[len(v) // 2] >= 2}
    data['tests'] = dict(sorted(heavy.items()))
    print(json.dumps(data, indent=1))


def main(argv):
    if argv[:1] == ['command'] and len(argv) == 2:
        run_command_partition(int(argv[1]))
    elif argv[:1] == ['weights'] and len(argv) > 1:
        weights(argv[1:])
    else:
        sys.exit('usage: ci-race.py command <partition> | weights <command-N.jsonl>...')


if __name__ == '__main__':
    main(sys.argv[1:])
