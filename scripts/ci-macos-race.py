#!/usr/bin/env python3
"""Run macOS-specific Go tests under the race detector on darwin runners.

Ubuntu already runs the full 3-partition race suite for cmd/looptender and all
packages. On macOS (10x billable rate), merge-queue and macOS-touching runs
exercise native darwin/unix platform behavior under -race without duplicating
every platform-independent unit and scenario test across three macOS runners.

Usage:
  ci-macos-race.py           Run platform packages, unix/darwin package subsets, and cmd macOS tests under -race
  ci-macos-race.py --check   Verify every darwin-targeted/guarded source and test file is covered (runs on any OS)
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

ALL_OS = ('linux', 'darwin', 'freebsd', 'netbsd', 'openbsd', 'solaris', 'aix',
          'js', 'plan9', 'ios', 'android', 'illumos', 'dragonfly', 'windows', 'unix')
KNOWN_ARCH = ('amd64', 'arm64', '386', 'arm', 'ppc64', 'ppc64le', 's390x',
              'riscv64', 'wasm', 'mips', 'mipsle', 'mips64', 'mips64le', 'loong64')
SKIP_PARTS = ('/testdata/', 'node_modules/', '.git/', 'vendor/', '.bb/', '.agents/')
PACKAGE_LINE = re.compile(r'^\s*package\s+\w+', re.M)
GO_BUILD_LINE = re.compile(r'^\s*//go:build\s+(.+?)\s*$', re.M)
DARWIN_WORD = re.compile(r'\bdarwin\b')
DARWIN_GOOS_LINE = re.compile(r'^[^\n]*\bruntime\.GOOS\b[^\n]*"darwin"')

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
    './internal/testexec',
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

# cmd/looptender test files exercising macOS launchd/service/daemon/socket/freeze/darwin behavior.
CMD_TEST_GLOBS = (
    'cmd/looptender/*_darwin*_test.go',
    'cmd/looptender/*_unix*_test.go',
    'cmd/looptender/service*_test.go',
    'cmd/looptender/router_service*_test.go',
    'cmd/looptender/control_freeze*_test.go',
    'cmd/looptender/legacy_retire*_test.go',
    'cmd/looptender/panel_claude_quota_test.go',
    'cmd/looptender/panel_account_selection_test.go',
    'cmd/looptender/replace_build_security_test.go',
)

TEST_FUNC = re.compile(r'^func ((?:Test|Example|Fuzz)\w*)\(', re.M)


def _eval_build_expr(expr, goos, *, cgo=True):
    """Evaluate a //go:build expression for one GOOS under -race (cgo enabled)."""
    def tag(m):
        name = m[0]
        if name == goos or (name == 'unix' and goos != 'windows'):
            return 'True'
        if name == 'cgo':
            return str(cgo)
        return str(bool(re.fullmatch(r'go1\.\d+', name)))

    py = re.sub(r'[A-Za-z_][\w.]*', tag, expr).replace('&&', ' and ').replace('||', ' or ').replace('!', ' not ')
    return bool(eval(py, {'__builtins__': {}}, {'True': True, 'False': False}))


def _header_before_package(text):
    m = PACKAGE_LINE.search(text)
    return text[:m.start()] if m else text


def _filename_os_tag(path):
    name = path.name
    if name.endswith('_test.go'):
        stem = name[:-len('_test.go')]
    elif name.endswith('.go'):
        stem = name[:-len('.go')]
    else:
        return ''
    parts = stem.split('_')
    if len(parts) >= 2 and parts[-1] in KNOWN_ARCH:
        parts = parts[:-1]
    if len(parts) >= 2 and parts[-1] in ALL_OS:
        return parts[-1]
    return ''


def builds_on_darwin(path, text, *, cgo=True):
    """Return True if path compiles on darwin under -race according to filename OS suffix and //go:build tags."""
    os_tag = _filename_os_tag(path)
    if os_tag and not _eval_build_expr(os_tag, 'darwin', cgo=cgo):
        return False
    header = _header_before_package(text)
    for expr in GO_BUILD_LINE.findall(header):
        if not _eval_build_expr(expr, 'darwin', cgo=cgo):
            return False
    return True


def _is_darwin_guarded(path, text):
    """Return True if a darwin-building file uses *_darwin* naming, a darwin build tag, or a darwin runtime.GOOS guard."""
    if '_darwin' in path.name:
        return True
    header = _header_before_package(text)
    if any(DARWIN_WORD.search(expr) for expr in GO_BUILD_LINE.findall(header)):
        return True
    for raw in text.splitlines():
        code = raw.split('//', 1)[0]
        if '"linux"' in code:
            continue
        if DARWIN_GOOS_LINE.search(code):
            return True
    return False


def collect_tests(root, patterns):
    names = set()
    for pat in patterns:
        for match in sorted(glob.glob(str(root / pat))):
            path = Path(match)
            text = path.read_text(errors='replace')
            if not builds_on_darwin(path, text):
                continue
            names.update(TEST_FUNC.findall(text))
    return sorted(names)


def darwin_files(root=ROOT):
    out = []
    for path in sorted(root.rglob('*.go')):
        rel = path.relative_to(root).as_posix()
        padded = '/' + rel
        if any(seg in padded for seg in SKIP_PARTS):
            continue
        text = path.read_text(errors='replace')
        if not builds_on_darwin(path, text):
            continue
        if _is_darwin_guarded(path, text):
            out.append(rel)
    return out


def verify_coverage(root=ROOT):
    """Fail if any darwin-targeted/guarded file sits outside our macOS platform test coverage."""
    covered_pkgs = set(PLATFORM_PACKAGES) | set(UNIX_TEST_GLOBS) | {'./cmd/looptender'}
    cmd_matched = {
        Path(m).relative_to(root).as_posix()
        for pat in CMD_TEST_GLOBS
        for m in glob.glob(str(root / pat))
    }
    uncovered = []
    for rel in darwin_files(root):
        pkg = './' + Path(rel).parent.as_posix()
        if pkg == './cmd/looptender':
            if rel.endswith('_test.go') and rel not in cmd_matched:
                uncovered.append((rel, pkg))
        elif pkg not in covered_pkgs:
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
