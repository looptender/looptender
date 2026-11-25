#!/usr/bin/env python3
"""Decide which runners and jobs a Checks run needs, and whether it can be skipped.

Writes `os` (JSON list for race partitions), `checks_os` (JSON list for
"Other checks"), `duplicate` (`true`/`false`), `scope`, and per-area boolean
flags (`run_race`, `run_checks`, `run_bb_plugin`, `run_windows`, `run_swift`,
`run_smoke`, `run_macos_race`) to $GITHUB_OUTPUT, and a reason to the job
summary.

  - Full 3-partition Go race coverage always runs on Ubuntu (`1x` cost). On
    macOS (`10x` cost), native darwin/unix `-race` coverage, `go build ./...`,
    smoke checks, and cached Swift menu checks run inside one `Other checks
    (macos-15)` runner instead of three redundant full-repo race VMs.
  - On pull requests (both draft and ready), changed files narrow what runs:
    - Docs/markdown-only PRs skip Go race, Windows, BB plugin, and macOS, while
      `Other checks (ubuntu-24.04)` runs doc table and CI script checks.
    - BB-plugin-only (`integrations/bb-plugin-keepgoing/`) PRs run only
      `BB plugin typecheck`.
    - Panel-UI-only (`cmd/looptender/dockui/`) PRs run `BB plugin typecheck`
      (jsdom/TS checks) and targeted panel tests on `ubuntu-24.04`.
    - Swift-only (`integrations/macos/`) PRs run `Other checks` on Ubuntu and
      macOS without Go race or Windows.
    - Non-macOS cross-platform Go PRs skip `macos-15` until the merge queue;
      pure darwin/unix/linux Go PRs skip Windows until the merge queue.
    - Marking a draft PR ready whose required checks already match the green
      PR run on the same SHA skips redundant re-runs (`duplicate=true`).
  - Merge queue runs (`merge_group`), non-duplicate `push` to `main`, manual
    runs, and any doubt run everything (`full`).

Environment: EVENT, ACTION (optional PR action), DRAFT, PR, SHA, REPO, GH_TOKEN.
Tests may set CI_PLAN_RUNS_JSON, CI_PLAN_PR_RUNS_JSON, and CI_PLAN_FILES_JSON.
"""
import json
import os
import subprocess
import sys

FULL = ['ubuntu-24.04', 'macos-15']
UBUNTU_ONLY = ['ubuntu-24.04']
MACOS_MENU_PREFIX = 'integrations/macos/'
MACOS_ONLY_PREFIXES = (MACOS_MENU_PREFIX,)
BB_PLUGIN_PREFIX = 'integrations/bb-plugin-keepgoing/'
DOCKUI_PREFIX = 'cmd/looptender/dockui/'
FILE_LIST_LIMIT = 3000

DOC_NOT_PREFIXES = ('.github/', 'cmd/', 'internal/', 'scripts/', 'schema/', 'examples/', '.bb/')
DOC_NOT_SEGMENTS = ('/testdata/', '/fixtures/', '/__fixtures__/', '/node_modules/')
DOC_ROOT_NAMES = ('LICENSE', 'LICENSING', 'NOTICE', 'COPYING', 'AUTHORS')

# internal/ packages that hold darwin/unix OS-specific code or process fixtures.
MACOS_SENSITIVE_INTERNAL = (
    'internal/hostcheck/',
    'internal/lock/',
    'internal/session/',
    'internal/userdir/',
    'internal/diskspace/',
    'internal/snapshot/',
    'internal/legacyretire/',
    'internal/router/channel/',
    'internal/router/core/',
    'internal/router/login/',
    'internal/telegram/',
    'internal/adapter/sessionpipe/',
    'internal/adapter/runner/',
    'internal/adapter/bb/',
    'internal/state/',
    'internal/supervise/',
    'internal/scenario/',
)


def is_doc_file(path):
    if not path:
        return False
    if path.startswith(DOC_NOT_PREFIXES) or path.startswith('testdata/'):
        return False
    if any(seg in '/' + path for seg in DOC_NOT_SEGMENTS):
        return False
    if path.startswith(('docs/', 'LICENSES/')) or path.endswith(('.md', '.markdown')):
        return True
    if '/' in path:
        return False
    for prefix in DOC_ROOT_NAMES:
        if path == prefix or path.startswith(prefix + '.') or (prefix in ('LICENSE', 'LICENSING') and path.startswith(prefix + '-')):
            return True
    return False


def is_unix_or_darwin_only_go(path):
    base = path.rsplit('/', 1)[-1]
    return any(tag in base for tag in ('_darwin.', '_darwin_test.', '_unix.', '_unix_test.', '_linux.', '_linux_test.'))


def needs_macos_checks(files):
    for f in files:
        if is_doc_file(f) or f.startswith(BB_PLUGIN_PREFIX) or f.startswith(DOCKUI_PREFIX):
            continue
        if f.startswith(MACOS_ONLY_PREFIXES) or '_darwin' in f or '_unix' in f:
            return True
        if f.startswith(('cmd/looptender/', 'scripts/', '.github/')) or f in ('go.mod', 'go.sum'):
            return True
        if f.startswith(MACOS_SENSITIVE_INTERNAL):
            return True
        if f.startswith('internal/'):
            continue
        return True
    return False


def needs_macos_race(files):
    for f in files:
        if is_doc_file(f) or f.startswith((BB_PLUGIN_PREFIX, DOCKUI_PREFIX, MACOS_MENU_PREFIX)):
            continue
        if '_darwin' in f or '_unix' in f or f.startswith(MACOS_SENSITIVE_INTERNAL):
            return True
        if f.startswith(('cmd/looptender/service', 'cmd/looptender/router_service', 'cmd/looptender/control_freeze',
                         'cmd/looptender/doctor_ac', 'cmd/looptender/start', 'cmd/looptender/adopt',
                         'cmd/looptender/replace_build', 'scripts/ci-macos-race', '.github/workflows/checks.yml')) or f in ('go.mod', 'go.sum'):
            return True
    return False


def needs_swift(files):
    for f in files:
        if f.startswith(MACOS_ONLY_PREFIXES):
            return True
        if f.startswith(('cmd/looptender/dock', 'cmd/looptender/panel', 'cmd/looptender/client',
                         'cmd/looptender/main', 'scripts/smoke-', '.github/workflows/checks.yml')) or f in ('go.mod', 'go.sum'):
            return True
    return False


def needs_windows(files):
    for f in files:
        if is_doc_file(f) or f.startswith((BB_PLUGIN_PREFIX, DOCKUI_PREFIX, MACOS_MENU_PREFIX)):
            continue
        if is_unix_or_darwin_only_go(f):
            continue
        return True
    return False


def needs_bb_plugin(files):
    for f in files:
        if is_doc_file(f) or f.startswith(MACOS_ONLY_PREFIXES):
            continue
        if f.startswith((BB_PLUGIN_PREFIX, DOCKUI_PREFIX, 'cmd/looptender/', 'scripts/ci-', '.github/workflows/checks.yml')) or f in ('go.mod', 'go.sum'):
            return True
    return False


def _flags(scope='full', run_race=True, run_checks=True, run_bb_plugin=True,
           run_windows=True, run_swift=True, run_smoke=True, run_macos_race=True):
    return {
        'scope': scope,
        'run_race': run_race,
        'run_checks': run_checks,
        'run_bb_plugin': run_bb_plugin,
        'run_windows': run_windows,
        'run_swift': run_swift,
        'run_smoke': run_smoke,
        'run_macos_race': run_macos_race,
    }


def plan(event, draft, passed_merge_group, changed_files=None, action='', passed_pull_request=None):
    """Return (os, duplicate, reason, checks_os, flags).

    os is for the 3-partition Go race suite (always Ubuntu; macOS runs its
    targeted darwin/unix race suite inside Other checks). checks_os is for
    "Other checks".
    """
    if event == 'pull_request':
        if draft not in ('true', 'false'):
            return UBUNTU_ONLY, False, 'Pull request draft state unknown; running everything.', FULL, _flags()
        try:
            files = changed_files() if changed_files else None
        except Exception as error:
            return UBUNTU_ONLY, False, f'Could not list the changed files ({error}); running everything.', FULL, _flags()
        if not files or len(files) >= FILE_LIST_LIMIT:
            return UBUNTU_ONLY, False, 'The changed files are unknown, empty or possibly cut short; running everything.', FULL, _flags()

        if all(is_doc_file(f) for f in files):
            flags = _flags(scope='docs', run_race=False, run_checks=True, run_bb_plugin=False,
                           run_windows=False, run_swift=False, run_smoke=False, run_macos_race=False)
            checks_systems = UBUNTU_ONLY
            reason = 'Docs-only pull request: running documentation and CI script checks on Ubuntu; skipping Go race, Windows, BB plugin and macOS.'
        elif all(f.startswith(BB_PLUGIN_PREFIX) or is_doc_file(f) for f in files):
            flags = _flags(scope='bb-plugin', run_race=False, run_checks=False, run_bb_plugin=True,
                           run_windows=False, run_swift=False, run_smoke=False, run_macos_race=False)
            checks_systems = UBUNTU_ONLY
            reason = 'BB-plugin-only pull request: running BB plugin typecheck and contract tests only.'
        elif all(f.startswith(DOCKUI_PREFIX) or is_doc_file(f) for f in files):
            flags = _flags(scope='dockui', run_race=False, run_checks=True, run_bb_plugin=True,
                           run_windows=False, run_swift=False, run_smoke=False, run_macos_race=False)
            checks_systems = UBUNTU_ONLY
            reason = 'Panel UI (dockui)-only pull request: running panel JS checks and dock/panel Go tests on Ubuntu.'
        elif all(f.startswith(MACOS_ONLY_PREFIXES) or is_doc_file(f) for f in files):
            flags = _flags(scope='macos-only', run_race=False, run_checks=True, run_bb_plugin=False,
                           run_windows=False, run_swift=True, run_smoke=False, run_macos_race=False)
            checks_systems = FULL
            reason = 'macOS-menu-only pull request: running Other checks on macOS and Ubuntu without full Go race or Windows.'
        elif draft == 'true':
            win = needs_windows(files)
            plugin = needs_bb_plugin(files)
            macos_files = any(f.startswith(MACOS_ONLY_PREFIXES) for f in files)
            checks_systems = FULL if macos_files else UBUNTU_ONLY
            flags = _flags(scope='full' if macos_files else 'go-no-macos', run_race=True, run_checks=True,
                           run_bb_plugin=plugin, run_windows=win, run_swift=macos_files,
                           run_smoke=True, run_macos_race=False)
            if macos_files:
                reason = ('Draft pull request with macOS menu changes: running macOS Other checks alongside Ubuntu '
                          '(race partitions on Ubuntu only; full macOS checks run on ready/merge queue).')
            else:
                reason = 'Draft pull request: macOS checks are skipped until marked ready or queued.'
        else:
            win = needs_windows(files)
            plugin = needs_bb_plugin(files)
            mac_checks = needs_macos_checks(files)
            swift = mac_checks and needs_swift(files)
            mac_race = mac_checks and needs_macos_race(files)
            checks_systems = FULL if mac_checks else UBUNTU_ONLY
            scope = 'full' if mac_checks else 'go-no-macos'
            flags = _flags(scope=scope, run_race=True, run_checks=True, run_bb_plugin=plugin,
                           run_windows=win, run_swift=swift, run_smoke=True, run_macos_race=mac_race)
            if mac_checks:
                reason = 'Ready pull request: running macOS Other checks (and full Ubuntu race); full macOS platform race runs in the merge queue.'
            else:
                reason = 'Ready pull request with portable Go changes only: skipping macOS checks until the merge queue.'

        if action == 'ready_for_review' and draft == 'false' and checks_systems == UBUNTU_ONLY and passed_pull_request:
            try:
                pr_run = passed_pull_request()
            except Exception:
                pr_run = None
            if pr_run:
                return UBUNTU_ONLY, True, (
                    f'Marked ready for review with no macOS checks needed; commit already passed pull_request run {pr_run}.'
                ), checks_systems, flags
        return UBUNTU_ONLY, False, reason, checks_systems, flags

    if event == 'push':
        try:
            run = passed_merge_group()
        except Exception as error:
            return UBUNTU_ONLY, False, f'Could not tell whether this commit already passed the merge queue ({error}); running everything.', FULL, _flags()
        if run:
            return UBUNTU_ONLY, True, f'This commit already passed Checks in merge-queue run {run}; its tree is identical, so this run does no work.', FULL, _flags()
    return UBUNTU_ONLY, False, 'Running everything (full Ubuntu race + macOS platform race + Windows + Swift).', FULL, _flags()


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


def passed_pull_request(repo, sha):
    answer = os.environ.get('CI_PLAN_PR_RUNS_JSON')
    if answer is None:
        answer = subprocess.check_output(
            ['gh', 'api', f'repos/{repo}/actions/workflows/checks.yml/runs?event=pull_request&head_sha={sha}&status=success&per_page=5'],
            text=True)
    runs = json.loads(answer).get('workflow_runs', [])
    for run in runs:
        if run.get('head_sha') == sha and run.get('event') == 'pull_request' and run.get('conclusion') == 'success':
            return run['id']
    return None


def pull_request_files(repo, number):
    """Every file a pull request changes, including the old name of a renamed one."""
    answer = os.environ.get('CI_PLAN_FILES_JSON')
    if answer is None:
        answer = subprocess.check_output(
            ['gh', 'api', '--paginate', '--slurp', f'repos/{repo}/pulls/{number}/files?per_page=100'], text=True)
    pages = json.loads(answer)
    if not isinstance(pages, list):
        raise ValueError(f'the files answer is not a list: {str(pages)[:80]}')
    names = []
    for page in pages:
        if not isinstance(page, list):
            raise ValueError(f'a page of the files answer is not a list: {str(page)[:80]}')
        for entry in page:
            if isinstance(entry, str):
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
    systems, duplicate, reason, checks_systems, flags = plan(
        event, os.environ.get('DRAFT', ''),
        lambda: passed_merge_group(os.environ['REPO'], os.environ['SHA']),
        lambda: pull_request_files(os.environ['REPO'], os.environ['PR']),
        action=os.environ.get('ACTION', ''),
        passed_pull_request=lambda: passed_pull_request(os.environ['REPO'], os.environ['SHA']))
    lines = [
        f'os={json.dumps(systems)}',
        f'checks_os={json.dumps(checks_systems)}',
        f'duplicate={"true" if duplicate else "false"}',
        f'scope={flags["scope"]}',
    ]
    for key in ('run_race', 'run_checks', 'run_bb_plugin', 'run_windows', 'run_swift', 'run_smoke', 'run_macos_race'):
        lines.append(f'{key}={"true" if flags[key] else "false"}')
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
