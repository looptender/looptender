#!/usr/bin/env python3
"""Run `go test ...` and fail if no test ran.

`go test -run PATTERN` exits 0 and prints "[no tests to run]" when a rename
leaves PATTERN matching nothing, so a step that pins tests by name can quietly
stop testing. Usage: ci-go-test-ran.py go test [flags] [packages]
Every output line is passed through. Exit status is go's, or 1 if go succeeded
but no package ran a test.
With -json output there are no `ok` result lines to read, so it exits 1 even on a
pass: that fails safe, and none of the wrapped steps uses -json.
"""
import re
import subprocess
import sys


def ran_any(lines):
    """True if some package result line is not '[no tests to run]'."""
    results = [l for l in lines if re.match(r'(ok|FAIL)\s+\S+\s', l)]
    return any('[no tests to run]' not in l for l in results)


def main(argv):
    if argv[:2] != ['go', 'test'] or len(argv) < 3:
        sys.exit('usage: ci-go-test-ran.py go test [flags] [packages]')
    process = subprocess.Popen(argv, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                               text=True, errors='replace')
    lines = []
    for line in process.stdout:
        sys.stdout.write(line)
        sys.stdout.flush()
        lines.append(line)
    code = process.wait()
    if code:
        return code
    if not ran_any(lines):
        print('ci-go-test-ran: go test succeeded but no test ran: the -run pattern '
              'matches nothing (was a test renamed?)', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main(sys.argv[1:]))
