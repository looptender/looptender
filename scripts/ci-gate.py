#!/usr/bin/env python3
"""The decision behind the required "Build and test" names.

Reads the `needs` context as JSON (env NEEDS) and exits 0 only if the run is
good: the plan job succeeded and every other job succeeded. The one exception
is a run the plan marked a duplicate (its commit already passed in the merge
queue): its jobs were skipped on purpose, and skipped is accepted for them
only. Failure and cancelled always fail, and a skipped job on any other run
fails: a job the gate cannot see succeed must not let a merge through.
"""
import json
import os
import sys


def verdict(needs):
    plan = needs.get('plan')
    if not plan or plan.get('result') != 'success':
        return False, ['plan: ' + (plan or {}).get('result', 'missing')]
    duplicate = plan.get('outputs', {}).get('duplicate') == 'true'
    lines, ok = [], True
    for name, need in sorted(needs.items()):
        result = need.get('result')
        good = result == 'success' or (duplicate and name != 'plan' and result == 'skipped')
        ok = ok and good
        lines.append(f'{name}: {result}' + ('' if good else '  <-- not accepted'))
    if len(needs) < 2:
        return False, lines + ['the gate waits for no job but the plan']
    return ok, lines


def main():
    ok, lines = verdict(json.loads(os.environ['NEEDS']))
    text = '\n'.join(lines)
    print(text)
    summary = os.environ.get('GITHUB_STEP_SUMMARY')
    if summary:
        with open(summary, 'a') as stream:
            stream.write('```\n' + text + '\n```\n')
    return 0 if ok else 1


if __name__ == '__main__':
    sys.exit(main())
