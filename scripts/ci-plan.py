#!/usr/bin/env python3
"""Decide which operating systems a Checks run needs, and whether it can be skipped.

Writes `os` (a JSON list for a job matrix) and `duplicate` (true/false) to
$GITHUB_OUTPUT, and a line to the job summary saying why.

  - A draft pull request runs Ubuntu and Windows only. macOS runs when the
    pull request is marked ready for review (the workflow listens for that),
    on every push to a ready pull request, and always in the merge queue, so
    nothing reaches main without a full run. Drafts that are pushed many times
    no longer take macOS runners.
  - A push to main whose commit already passed a merge-queue Checks run is a
    duplicate: the queue fast-forwards main to the commit it tested, so the
    tree is identical. Any other push (a direct push, a different commit) runs
    everything.
  - Everything else, including every merge-queue run, runs everything.
Any error answers "run everything".

Environment: EVENT, DRAFT (the pull request's draft flag, or empty), SHA,
REPO, GH_TOKEN. For tests, CI_PLAN_RUNS_JSON holds the API answer.
"""
import json
import os
import subprocess
import sys

FULL = ['ubuntu-24.04', 'macos-15']
UBUNTU_ONLY = ['ubuntu-24.04']


def plan(event, draft, passed_merge_group):
    """Return (os list, duplicate, reason). passed_merge_group is called only for a push."""
    if event == 'pull_request' and draft == 'true':
        return UBUNTU_ONLY, False, ('Draft pull request: macOS jobs are skipped. They run when it is marked ready, '
                                    'on later pushes to it, and in the merge queue.')
    if event == 'push':
        try:
            run = passed_merge_group()
        except Exception as error:  # any doubt means run everything
            return FULL, False, f'Could not tell whether this commit already passed the merge queue ({error}); running everything.'
        if run:
            return FULL, True, f'This commit already passed Checks in merge-queue run {run}; its tree is identical, so this run does no work.'
    return FULL, False, 'Running everything.'


def passed_merge_group(repo, sha):
    answer = os.environ.get('CI_PLAN_RUNS_JSON')
    if answer is None:
        answer = subprocess.check_output(
            ['gh', 'api', f'repos/{repo}/actions/workflows/checks.yml/runs?event=merge_group&head_sha={sha}&status=success&per_page=1'],
            text=True)
    runs = json.loads(answer).get('workflow_runs', [])
    for run in runs:
        if run.get('head_sha') == sha and run.get('event') == 'merge_group' and run.get('conclusion') == 'success':
            return run['id']
    return None


def main():
    event = os.environ.get('EVENT', '')
    systems, duplicate, reason = plan(event, os.environ.get('DRAFT', ''),
                                      lambda: passed_merge_group(os.environ['REPO'], os.environ['SHA']))
    lines = [f'os={json.dumps(systems)}', f'duplicate={"true" if duplicate else "false"}']
    # The outputs go to the file GitHub reads, never to stdout: only the
    # reason is printed, and a step that redirected stdout there would corrupt it.
    out = os.environ.get('GITHUB_OUTPUT')
    with open(out, 'a') if out else sys.stderr as target:
        target.write('\n'.join(lines) + '\n')
    summary = os.environ.get('GITHUB_STEP_SUMMARY')
    if summary:
        with open(summary, 'a') as stream:
            stream.write(reason + '\n')
    print(reason)


if __name__ == '__main__':
    main()
