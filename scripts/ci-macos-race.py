#!/usr/bin/env python3
"""Run macOS-specific Go tests under the race detector on darwin runners.

Ubuntu already runs the full 3-partition race suite for cmd/looptender and all
packages. On macOS (10x billable rate), merge-queue and macOS-touching runs
exercise native darwin/unix platform behavior under -race without duplicating
every platform-independent unit and scenario test across three macOS runners.

Usage:
  ci-macos-race.py           Run platform packages, unix/darwin package subsets, and cmd macOS tests under -race
  ci-macos-race.py --check   Verify every *_darwin* source/test file in the tree is covered (runs on any OS)
"""
import collections
import glob
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import time

ROOT = Path(os.environ.get('CI_MACOS_RACE_ROOT') or Path(__file__).resolve().parents[1])

# Packages in internal/ whose primary behavior is OS, process, filesystem,
# launchd, APFS, or Unix socket handling. Run whole package under -race.
PLATFORM_PACKAGES = (
    './internal/hostcheck',
    './internal/lock',
    './internal/session',
    './internal/userdir',
    './internal/diskspace',
    './internal/snapshot',
    './internal/legacyretire',
    './internal/router/channel',
    './internal/router/core',
    './internal/router/login',
    './internal/telegram',
    './internal/adapter/sessionpipe',
)

# Larger internal/ packages that contain platform-specific *_darwin*_test.go or
# *_unix*_test.go files alongside large portable suites; run their unix/darwin tests.
UNIX_TEST_GLOBS = {
    './internal/adapter/runner': ('internal/adapter/runner/*_darwin*_test.go', 'internal/adapter/runner/*_unix*_test.go'),
    './internal/adapter/bb': ('internal/adapter/bb/*_darwin*_test.go', 'internal/adapter/bb/*_unix*_test.go'),
    './internal/config': ('internal/config/*_darwin*_test.go', 'internal/config/*_unix*_test.go'),
    './internal/registry': ('internal/registry/*_darwin*_test.go', 'internal/registry/*_unix*_test.go'),
    './internal/report': ('internal/report/*_darwin*_test.go', 'internal/report/*_unix*_test.go'),
    './internal/probe': ('internal/probe/*_darwin*_test.go', 'internal/probe/*_unix*_test.go'),
    './internal/netretry': ('internal/netretry/*_darwin*_test.go', 'internal/netretry/*_unix*_test.go'),
    './internal/state': ('internal/state/*_darwin*_test.go', 'internal/state/*_unix*_test.go'),
    './internal/supervise': ('internal/supervise/*_darwin*_test.go', 'internal/supervise/*_unix*_test.go'),
}

# cmd/looptender test files exercising macOS launchd/service/daemon/socket/freeze/unix behavior.
CMD_TEST_GLOBS = (
    'cmd/looptender/*_darwin*_test.go',
    'cmd/looptender/*_unix*_test.go',
    'cmd/looptender/service*_test.go',
    'cmd/looptender/router_service*_test.go',
    'cmd/looptender/control_freeze*_test.go',
)

TEST_FUNC = re.compile(r'^func ((?:Test|Example|Fuzz)\w*)\(', re.M)


def collect_tests(root, patterns):
    names = set()
    for pat in patterns:
        for match in sorted(glob.glob(str(root / pat))):
            text = Path(match).read_text(errors='replace')
            names.update(TEST_FUNC.findall(text))
    return sorted(names)


def darwin_files(root=ROOT):
    out = []
    for path in sorted(root.rglob('*_darwin*.go')):
        rel = path.relative_to(root).as_posix()
        if '/testdata/' in '/' + rel or 'node_modules/' in rel:
            continue
        out.append(rel)
    return out


def verify_coverage(root=ROOT):
    """Fail if any *_darwin*.go file sits outside our macOS platform test coverage."""
    covered_pkgs = set(PLATFORM_PACKAGES) | set(UNIX_TEST_GLOBS) | {'./cmd/looptender'}
    uncovered = []
    for rel in darwin_files(root):
        pkg = './' + Path(rel).parent.as_posix()
        if pkg not in covered_pkgs:
            uncovered.append((rel, pkg))
        elif pkg in UNIX_TEST_GLOBS and rel.endswith('_test.go'):
            matched = {
                Path(m).relative_to(root).as_posix()
                for pat in UNIX_TEST_GLOBS[pkg]
                for m in glob.glob(str(root / pat))
            }
            if rel not in matched:
                uncovered.append((rel, pkg))
    cmd_tests = collect_tests(root, CMD_TEST_GLOBS)
    if not cmd_tests:
        raise ValueError('no cmd/looptender macOS tests discovered')
    if uncovered:
        lines = [f'{rel} ({pkg}) is not covered by scripts/ci-macos-race.py' for rel, pkg in uncovered]
        raise ValueError('; '.join(lines))
    return len(cmd_tests)


def verify_test2json_execution(log_path, expected_names):
    started, ended = [], []
    for line in log_path.read_text(errors='replace').splitlines():
        try:
            ev = __import__('json').loads(line)
        except ValueError:
            continue
        name = ev.get('Test', '')
        if not name or '/' in name:
            continue
        if ev.get('Action') == 'run':
            started.append(name)
        elif ev.get('Action') in ('pass', 'skip', 'fail'):
            ended.append(name)
    want = collections.Counter(expected_names)
    if collections.Counter(started) != want or collections.Counter(ended) != want:
        raise ValueError(f'{log_path}: executed tests differ from expected macOS test inventory')


def run_suite(root=ROOT):
    verify_coverage(root)
    started = time.monotonic()
    print(f'::group::macOS platform packages under -race ({len(PLATFORM_PACKAGES)} packages)', flush=True)
    subprocess.run(['go', 'test', '-race', '-count=1', *PLATFORM_PACKAGES], cwd=root, check=True)
    print('::endgroup::', flush=True)

    for pkg, globs in UNIX_TEST_GLOBS.items():
        names = collect_tests(root, globs)
        if not names:
            continue
        pattern = '^(' + '|'.join(re.escape(n) for n in names) + ')$'
        print(f'::group::macOS unix/darwin tests in {pkg} ({len(names)} tests)', flush=True)
        subprocess.run([sys.executable, str(root / 'scripts/ci-go-test-ran.py'),
                        'go', 'test', '-race', pkg, '-run', pattern, '-count=1'], cwd=root, check=True)
        print('::endgroup::', flush=True)

    cmd_names = collect_tests(root, CMD_TEST_GLOBS)
    pattern = '^(' + '|'.join(re.escape(n) for n in cmd_names) + ')$'
    print(f'::group::macOS cmd/looptender OS/service tests under -race ({len(cmd_names)} tests)', flush=True)
    with tempfile.TemporaryDirectory(prefix='ci-macos-race-') as build:
        binary = str(Path(build) / 'command.test')
        log = Path(build) / 'command-macos.jsonl'
        subprocess.run(['go', 'test', '-race', '-c', '-o', binary, './cmd/looptender'], cwd=root, check=True)
        with open(log, 'w') as out:
            proc = subprocess.run(
                ['go', 'tool', 'test2json', '-t', '-p', 'github.com/looptender/looptender/cmd/looptender',
                 binary, '-test.v=test2json', '-test.count=1', '-test.timeout=480s', '-test.run=' + pattern],
                cwd=root / 'cmd/looptender', stdout=out, stderr=subprocess.STDOUT)
        if proc.returncode != 0:
            print(log.read_text(errors='replace'), file=sys.stderr)
            raise RuntimeError(f'macOS cmd/looptender targeted race suite failed ({proc.returncode})')
        verify_test2json_execution(log, cmd_names)
    print('::endgroup::', flush=True)
    elapsed = time.monotonic() - started
    msg = f'macOS targeted race suite passed ({len(PLATFORM_PACKAGES)} packages + {len(cmd_names)} cmd OS/service tests) in {elapsed:.0f}s.'
    print(msg, flush=True)
    summary = os.environ.get('GITHUB_STEP_SUMMARY')
    if summary:
        with open(summary, 'a') as s:
            s.write(msg + '\n\n')


def main(argv):
    if argv == ['--check']:
        n = verify_coverage(ROOT)
        print(f'ci-macos-race: all darwin files covered ({n} cmd/looptender OS/service tests selected).')
        return 0
    if not argv:
        run_suite(ROOT)
        return 0
    sys.exit('usage: ci-macos-race.py [--check]')


if __name__ == '__main__':
    sys.exit(main(sys.argv[1:]))
