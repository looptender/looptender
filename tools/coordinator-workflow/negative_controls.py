"""Bounded offline behavioral mutations of private copies; no production edits."""
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
from fixture_environment import isolated_environment

ROOT = Path(__file__).resolve().parents[2]
SCRIPTS = Path('integrations/bb-plugin-keepgoing/skills/keepgoing/scripts')
TESTS = Path('tools/coordinator-workflow')
CASES = [
    ('unknown reservation', 'coordinator_state.py',
     '    if any(a["state"] in {"unknown", "mapping_pending"} for a in attempts):\n        return None, dict(base, action="reconcile_unknown_attempt")\n', '',
     'test_state.StateTests.test_unknown_survives_restart_and_new_grant_with_all_reservations'),
    ('artifact acceptance', 'coordinator_state.py',
     '        verify_artifacts(receipt["artifacts"])', '        pass  # private negative control',
     'test_state.StateTests.test_complete_requires_current_verified_artifact_then_dispatches_independent_next'),
    ('source drift', 'coordinator.py',
     '        require(file_hash(item["path"])[0] == item["sha256"], "source_changed")', '        pass  # private negative control',
     'test_commands.CommandTests.test_binding_drift_pending_input_and_source_drift_do_not_create'),
]
for label, filename, old, new, test in CASES:
    with tempfile.TemporaryDirectory(prefix='coordinator-negative-', dir='/tmp') as temporary:
        root = Path(temporary)
        (root / SCRIPTS).mkdir(parents=True)
        (root / TESTS).mkdir(parents=True)
        for source in (ROOT / SCRIPTS).glob('*.py'): shutil.copyfile(source, root / SCRIPTS / source.name)
        for source in (ROOT / TESTS).glob('*.py'): shutil.copyfile(source, root / TESTS / source.name)
        p = root / SCRIPTS / filename
        source = p.read_text()
        if source.count(old) != 1: raise SystemExit('mutation boundary changed: ' + label)
        p.write_text(source.replace(old, new))
        result = subprocess.run([sys.executable, '-B', '-m', 'unittest', test], cwd=root / TESTS,
                                env=isolated_environment(root / 'environment'), capture_output=True, text=True, timeout=25)
        # A syntax/import/infrastructure error is not evidence of a useful test.
        if result.returncode == 0 or 'FAIL:' not in result.stderr or 'AssertionError' not in result.stderr:
            print(result.stdout + result.stderr)
            raise SystemExit('negative control did not fail behaviorally: ' + label)
        print(label + ': intended behavioral assertion failed')
