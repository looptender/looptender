"""Regressions for native docs scope, plan-based skipping, and the race runner entry point."""
from pathlib import Path
import re
import unittest

ROOT = Path(__file__).resolve().parents[1]
WORKFLOW = (ROOT / '.github/workflows/checks.yml').read_text()
SCOPE = "steps.scope.outputs.scope != 'docs'"

JOB_FLAGS = {
    'bb-plugin': 'run_bb_plugin',
    'windows-acl': 'run_windows',
    'windows-processes': 'run_windows',
    'race': 'run_race',
    'checks': 'run_checks',
}


def validate(text):
    for job in ('windows-acl', 'windows-processes'):
        match = re.search(r'^  ' + job + r':\n(.*?)(?=^  [a-z][a-z-]*:|\Z)', text, re.M | re.S)
        assert match, f'missing {job}'
        steps = re.split(r'^      - ', match[1], flags=re.M)[1:]
        native = [step for step in steps if re.search(r'go (test|vet) ', step)]
        assert native, f'no native steps in {job}'
        for step in native:
            condition = re.search(r'^        if: (.+)$', step, re.M)
            assert condition and condition[1] in (SCOPE, f'always() && ({SCOPE})'), step
    steps = re.split(r'^      - ', text, flags=re.M)[1:]
    for prefix, command in (('name: Run one disjoint partition of the command package, and a share of the other packages, under the race detector\n',
                             r'python3 -u scripts/ci-race.py command \$\{\{ matrix.part \}\}'),):
        race = [s for s in steps if s.startswith(prefix)]
        assert len(race) == 1, f'missing or duplicate native race step: {prefix}'
        assert re.search(r'^        run: ' + command + r'$', race[0], re.M), 'native runner failure must propagate'
        assert f"if: {SCOPE} || matrix.os == 'ubuntu-24.04'" in race[0]
    mac_race = [s for s in steps if s.startswith('name: Run macOS-specific Go packages and OS/service tests under the race detector\n')]
    assert len(mac_race) == 1, 'missing or duplicate macOS targeted race step'
    assert re.search(r'^        run: python3 -u scripts/ci-macos-race\.py$', mac_race[0], re.M)
    gate = re.search(r'^  go:\n(.*?)(?=^  [a-z][a-z-]*:|\Z)', text, re.M | re.S)
    assert gate and 'if: ${{ !cancelled() }}' in gate[1], 'the required name must gate on every job'
    assert 'fail-fast: false' in gate[1], 'a failing gate must not cancel the other'
    assert 'Build and test (${{ matrix.os }})' in gate[1], 'branch protection requires this name'
    assert not re.search(r'^        run: go test -race ./cmd/looptender -run', text, re.M), 'unwrapped go test -race -run is not allowed'
    # The gate must wait for every other job: a job it does not wait for cannot
    # block a merge, because only the gate's names are required.
    jobs = set(re.findall(r'^  ([a-z][a-z-]*):\n', text.split('\njobs:\n', 1)[1], re.M)) - {'go'}
    needs = re.search(r'^    needs: \[([a-z, -]+)\]$', gate[1], re.M)
    assert needs and set(n.strip() for n in needs[1].split(',')) == jobs, f'the gate must need exactly {sorted(jobs)}'
    assert re.search(r'^        run: python3 scripts/ci-gate\.py$', gate[1], re.M), 'the gate must use the tested decision script'
    assert 'NEEDS: ${{ toJSON(needs) }}' in gate[1], 'the gate must read every needed job result'
    # Every job obeys the plan: it needs it, skips on duplicate or explicit plan flag,
    # and matrix jobs take their operating systems from it.
    body = text.split('\njobs:\n', 1)[1]
    for job in sorted(jobs - {'plan'}):
        part = re.search(r'^  ' + job + r':\n(.*?)(?=^  [a-z][a-z-]*:|\Z)', body, re.M | re.S)[1]
        flag = JOB_FLAGS[job]
        want_if = f"if: needs.plan.outputs.duplicate != 'true' && needs.plan.outputs.{flag} != 'false'"
        assert re.search(r'^    needs: plan$', part, re.M) and want_if in part, f'{job} must follow the plan ({want_if})'
    for job, output in (('race', 'os'), ('checks', 'checks_os')):
        part = re.search(r'^  ' + job + r':\n(.*?)(?=^  [a-z][a-z-]*:|\Z)', body, re.M | re.S)[1]
        assert 'fail-fast: false' in part, f'a failing {job} leg must not cancel the others'
        assert 'os: ${{ fromJSON(needs.plan.outputs.' + output + ') }}' in part, f'{job} must take its operating systems from the plan'
    assert 'types: [opened, synchronize, reopened, ready_for_review]' in text, 'macOS must run when a draft is marked ready'
    # A pinned -run pattern that matches nothing must fail, not pass silently.
    for step in steps:
        for line in step.splitlines():
            if re.search(r'\bgo test\b.* -run ', line) and 'go test' in line:
                assert re.search(r'python3? scripts/ci-go-test-ran\.py go test', line), f'pinned go test must go through the wrapper: {line.strip()}'
    for script in ('ci-scope-test.sh', 'check-contributions-test.sh', 'ci-timeout-note-test.sh',
                   'ci-workflow-test.py', 'ci-plan-test.py', 'ci-go-test-ran-test.py',
                   'ci-windows-inventory.py', 'ci-macos-race.py'):
        assert text.index(script) < text.index('run: go vet ./...'), f'{script} must fail before expensive checks'


class WorkflowTests(unittest.TestCase):
    def test_current_workflow(self):
        validate(WORKFLOW)

    def test_unguarded_assignment_rejected(self):
        bad = WORKFLOW.replace('name: Verify explicit assignment lifecycle and coordination records on Windows\n        if: ' + SCOPE,
                               'name: Verify explicit assignment lifecycle and coordination records on Windows')
        with self.assertRaises(AssertionError):
            validate(bad)

    def test_accidentally_skipped_native_coverage_rejected(self):
        with self.assertRaises(AssertionError):
            validate(WORKFLOW.replace("run: go vet ./cmd/looptender\n        if: " + SCOPE, "run: go vet ./cmd/looptender\n        if: steps.scope.outputs.scope == 'docs'", 1))

    def test_missing_or_nonpropagating_native_runner_rejected(self):
        for replacement in ('echo skipped', 'python3 -u scripts/ci-race.py command ${{ matrix.part }} || true', 'go test -race ./cmd/looptender'):
            with self.subTest(replacement=replacement), self.assertRaises(AssertionError):
                validate(WORKFLOW.replace('python3 -u scripts/ci-race.py command ${{ matrix.part }}', replacement))

    def test_a_gate_that_forgets_a_job_or_tolerates_failure_is_rejected(self):
        for old, new in (('run: python3 scripts/ci-gate.py', 'run: true'),
                         ('if: ${{ !cancelled() }}', ''),
                         ('fail-fast: false # each gate reports its own result', '')):
            with self.subTest(old=old), self.assertRaises(AssertionError):
                validate(WORKFLOW.replace(old, new, 1))

    def test_a_gate_missing_a_job_is_rejected(self):
        for job in ('bb-plugin', 'windows-acl', 'windows-processes', 'race', 'checks'):
            with self.subTest(job=job), self.assertRaises(AssertionError):
                validate(re.sub(r'^(    needs: \[.*)\b' + job + r'\b,? ?(.*\])$', r'\1\2', WORKFLOW, count=1, flags=re.M)
                         .replace(', ]', ']'))

    def test_a_job_that_ignores_the_plan_or_the_ready_trigger_is_rejected(self):
        for old, new in (("    needs: plan\n    if: needs.plan.outputs.duplicate != 'true' && needs.plan.outputs.run_bb_plugin != 'false'", "    if: always()"),
                         ('os: ${{ fromJSON(needs.plan.outputs.os) }}', 'os: [ubuntu-24.04, macos-15]'),
                         ('os: ${{ fromJSON(needs.plan.outputs.checks_os) }}', 'os: ${{ fromJSON(needs.plan.outputs.os) }}'),
                         ('synchronize, reopened, ready_for_review]', 'synchronize, reopened]')):
            with self.subTest(old=old[:40]), self.assertRaises(AssertionError):
                validate(WORKFLOW.replace(old, new, 1))

    def test_a_matrix_without_fail_fast_false_is_rejected(self):
        for job in ('race', 'checks'):
            with self.subTest(job=job), self.assertRaises(AssertionError):
                i = WORKFLOW.index(f'\n  {job}:\n')
                j = WORKFLOW.index('      fail-fast: false', i)
                validate(WORKFLOW[:j] + '      fail-fast: true' + WORKFLOW[WORKFLOW.index('\n', j):])

    def test_an_unwrapped_pinned_go_test_is_rejected(self):
        with self.assertRaises(AssertionError):
            validate(WORKFLOW.replace('python scripts/ci-go-test-ran.py go test ./cmd/looptender -run', 'go test ./cmd/looptender -run', 1))

    def test_redundant_static_guard_execution_rejected(self):
        with self.assertRaises(AssertionError):
            validate(WORKFLOW + "\n        run: go test -race ./cmd/looptender -run '^TestDock'\n")


if __name__ == '__main__':
    unittest.main()
