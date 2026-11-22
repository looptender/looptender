#!/usr/bin/env python3
"""Fail if a Windows-only Go test is run by no pinned Windows step.

Windows has no full race suite: its jobs run named tests (`go test -run
'^(A|B)'`) in listed packages. A Windows-only test, one in a file that builds
only on windows, runs nowhere else, so a rename that leaves it outside every
pattern would silently stop it running. This lists those tests and checks each
is selected by a step of a Windows job whose packages include its package (or
by a step with those packages and no -run, which runs the whole package).

Usage: ci-windows-inventory.py [--list]
"""
import os
import re
import shlex
import sys
from pathlib import Path

# CI_WINDOWS_INVENTORY_ROOT lets the test point it at a fixture tree.
ROOT = Path(os.environ.get('CI_WINDOWS_INVENTORY_ROOT') or Path(__file__).resolve().parents[1])
WORKFLOW = ROOT / '.github/workflows/checks.yml'
WINDOWS_JOBS = ('windows-acl', 'windows-processes')
TEST_FUNC = re.compile(r'^func ((?:Test|Example|Fuzz)\w*)\(', re.M)
OTHER_OS = ('linux', 'darwin', 'freebsd', 'netbsd', 'openbsd', 'solaris', 'aix', 'js', 'plan9', 'ios', 'android', 'illumos', 'dragonfly')


def builds_on(expr, goos):
    """Evaluate a //go:build expression for one GOOS (other tags are false, goN.M true)."""
    def tag(m):
        name = m[0]
        return str(name == goos or (name == 'unix' and goos != 'windows') or bool(re.fullmatch(r'go1\.\d+', name)))
    python = re.sub(r'[A-Za-z_][\w.]*', tag, expr).replace('&&', ' and ').replace('||', ' or ').replace('!', ' not ')
    return eval(python, {'__builtins__': {}}, {'True': True, 'False': False})


def windows_only(path, text):
    """True if the file builds on windows and on no other OS."""
    exprs = []
    for line in text.splitlines():
        if line.startswith('//go:build '):
            exprs.append(line[len('//go:build '):].strip())
        elif line.startswith('package '):
            break
    name = path.name[:-len('_test.go')]
    suffix_os = name.rsplit('_', 1)[-1] if '_' in name else ''
    for suffix in (suffix_os, name.rsplit('_', 2)[-2] if name.count('_') >= 2 else ''):
        if suffix == 'windows':
            exprs.append('windows')
        elif suffix in OTHER_OS:
            exprs.append(suffix)
    if not exprs:
        return False
    if not all(builds_on(e, 'windows') for e in exprs):
        return False
    return not any(all(builds_on(e, other) for e in exprs) for other in OTHER_OS)


def inventory(root=ROOT):
    tests = []
    for path in sorted(root.rglob('*_test.go')):
        if 'testdata' in path.parts or 'node_modules' in path.parts:
            continue
        text = path.read_text(errors='replace')
        if windows_only(path, text):
            package = './' + path.parent.relative_to(root).as_posix()
            tests += [(package, name, path.relative_to(root).as_posix()) for name in TEST_FUNC.findall(text)]
    return tests


def steps(workflow):
    """(packages, run pattern or None) for each go test command in a Windows job."""
    found = []
    for job in WINDOWS_JOBS:
        match = re.search(r'^  ' + job + r':\n(.*?)(?=^  [a-z][a-z-]*:|\Z)', workflow, re.M | re.S)
        if not match:
            raise ValueError(f'missing Windows job {job}')
        for line in match[1].splitlines():
            if not re.search(r'\bgo test\b', line):
                continue
            words = shlex.split(line[line.index('go test') + len('go test'):], posix=True)
            packages, pattern = [], None
            it = iter(words)
            for word in it:
                if word == '-run':
                    pattern = next(it)
                elif word.startswith('./'):
                    packages.append(word)
            found.append((packages, pattern))
    return found


def uncovered(tests, commands):
    missing = []
    for package, name, path in tests:
        ok = any(package in packages and (pattern is None or re.search(pattern, name))
                 for packages, pattern in commands)
        if not ok:
            missing.append((package, name, path))
    return missing


def main(argv):
    tests = inventory()
    if '--list' in argv:
        for package, name, path in tests:
            print(package, name, path)
        return 0
    if not tests:
        print('no Windows-only tests found: the inventory is broken', file=sys.stderr)
        return 1
    missing = uncovered(tests, steps(WORKFLOW.read_text()))
    for package, name, path in missing:
        print(f'{name} ({path}) is Windows-only and matched by no pinned Windows step for {package}', file=sys.stderr)
    print(f'{len(tests) - len(missing)} of {len(tests)} Windows-only tests are selected by a pinned step.')
    return 1 if missing else 0


if __name__ == '__main__':
    sys.exit(main(sys.argv[1:]))
