#!/usr/bin/env python3
"""Decide which operating systems a Checks run needs, and whether it can be skipped.

Writes `os` (a JSON list for the race partitions' matrix), `checks_os` (the
same for the "Other checks" job) and `duplicate` (true/false) to
$GITHUB_OUTPUT, and a line to the job summary saying why.

  - A draft pull request runs Ubuntu and Windows only. macOS runs when the
    pull request is marked ready for review (the workflow listens for that),
    on every push to a ready pull request, and always in the merge queue, so
    nothing reaches main without a full run. Drafts that are pushed many times
    no longer take macOS runners.
  - A ready (non-draft) pull request run takes one macOS job, "Other checks"
    (Swift build and tests, the macOS-only checks), and not the three macOS race
    partitions. The race partitions run on macOS only in the merge queue (and
    on a manual full run), which is the enforced proof before anything lands.
    GitHub gives this organisation about five concurrent macOS jobs, and a full
    run takes four. Only an explicit non-draft pull request is reduced: an
    unknown draft flag is doubt and runs everything.
  - The exception: a draft that changes files only the macOS "Other checks"
    job exercises (the Swift menu under integrations/macos: `swift build`,
    `swift test` and the contract smoke check) also runs that one job on macOS,
    never the three race partitions. Without it a Swift author could not test
    a draft at all. If the changed files cannot be listed, or the list is empty
    or may be cut short, that is doubt, and the run takes everything.
  - A push to main whose commit already passed a merge-queue Checks run is a
    duplicate: the queue fast-forwards main to the commit it tested, so the
    tree is identical. Any other push (a direct push, a different commit) runs
    everything.
  - Everything else, including every merge-queue run, runs everything.
Any error answers "run everything".

Environment: EVENT, DRAFT (the pull request's draft flag, or empty), PR (its
number), SHA, REPO, GH_TOKEN. For tests, CI_PLAN_RUNS_JSON holds the runs API
answer and CI_PLAN_FILES_JSON the pull request's changed files.
"""
import json
import os
import subprocess
import sys

FULL = ['ubuntu-24.04', 'macos-15']
UBUNTU_ONLY = ['ubuntu-24.04']
# Paths that only the macOS "Other checks" job exercises. Nothing else on this
# list: Go code is exercised by the Ubuntu race partitions too.
MACOS_ONLY_PREFIXES = ('integrations/macos/',)
# The pull request files API stops at this many files: a list this long may be cut short.
FILE_LIST_LIMIT = 3000


def plan(event, draft, passed_merge_group, changed_files=None):
    """Return (os list, duplicate, reason, checks_os).

    os is for the race partitions, checks_os for "Other checks". passed_merge_group
    is called only for a push, changed_files only for a draft pull request.
    """
    if event == 'pull_request' and draft == 'true':
        try:
            files = changed_files() if changed_files else None
        except Exception as error:  # any doubt means run everything
            return FULL, False, f'Could not list the changed files ({error}); running everything.', FULL
        if not files or len(files) >= FILE_LIST_LIMIT:
            return FULL, False, 'The changed files are unknown, empty or possibly cut short; running everything.', FULL
        macos = sorted(f for f in files if f.startswith(MACOS_ONLY_PREFIXES))
        if macos:
            return UBUNTU_ONLY, False, ('Draft pull request that changes macOS-only files '
                                        f'({", ".join(macos[:3])}{", ..." if len(macos) > 3 else ""}): '
                                        'the macOS "Other checks" job runs, the macOS race partitions do not. '
                                        'They run when it is marked ready, on later pushes and in the merge queue.'), FULL
        return UBUNTU_ONLY, False, ('Draft pull request: macOS jobs are skipped. They run when it is marked ready, '
                                    'on later pushes to it, and in the merge queue.'), UBUNTU_ONLY
    if event == 'pull_request' and draft == 'false':
        return UBUNTU_ONLY, False, ('Ready pull request: the macOS "Other checks" job runs; the macOS race partitions '
                                    'run in the merge queue, which always runs everything.'), FULL
    if event == 'push':
        try:
            run = passed_merge_group()
        except Exception as error:  # any doubt means run everything
            return FULL, False, f'Could not tell whether this commit already passed the merge queue ({error}); running everything.', FULL
        if run:
            return FULL, True, f'This commit already passed Checks in merge-queue run {run}; its tree is identical, so this run does no work.', FULL
    return FULL, False, 'Running everything.', FULL


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


def pull_request_files(repo, number):
    """Every file a pull request changes, including the old name of a renamed one."""
    answer = os.environ.get('CI_PLAN_FILES_JSON')
    if answer is None:
        answer = subprocess.check_output(
            ['gh', 'api', '--paginate', '--slurp', f'repos/{repo}/pulls/{number}/files?per_page=100'], text=True)
    pages = json.loads(answer)
    # An error body is a JSON object, not a list: that is doubt, never a list of names.
    if not isinstance(pages, list):
        raise ValueError(f'the files answer is not a list: {str(pages)[:80]}')
    names = []
    for page in pages:
        if not isinstance(page, list):
            raise ValueError(f'a page of the files answer is not a list: {str(page)[:80]}')
        for entry in page:
            if isinstance(entry, str):  # the test hook's plain names
                names.append(entry)
                continue
            if not isinstance(entry, dict) or not isinstance(entry.get('filename'), str):
                raise ValueError(f'a changed-file entry has no filename: {str(entry)[:80]}')
            names.append(entry['filename'])
            if isinstance(entry.get('previous_filename'), str):
                names.append(entry['previous_filename'])
    return names


def main():
    event = os.environ.get('EVENT', '')
    systems, duplicate, reason, checks_systems = plan(
        event, os.environ.get('DRAFT', ''),
        lambda: passed_merge_group(os.environ['REPO'], os.environ['SHA']),
        lambda: pull_request_files(os.environ['REPO'], os.environ['PR']))
    lines = [f'os={json.dumps(systems)}', f'checks_os={json.dumps(checks_systems)}',
             f'duplicate={"true" if duplicate else "false"}']
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
