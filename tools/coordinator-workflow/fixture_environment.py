"""Test-only allowlist. Never used by the production caller adapter."""
from pathlib import Path


def isolated_environment(root):
    root = Path(root).resolve()
    paths = {name: root / name for name in ('home', 'config', 'data', 'cache', 'state', 'run', 'tmp', 'registry', 'sessions')}
    for path in paths.values():
        path.mkdir(mode=0o700, parents=True, exist_ok=True)
    env = {
        'PATH': '/usr/bin:/bin', 'HOME': str(paths['home']), 'USERPROFILE': str(paths['home']),
        'XDG_CONFIG_HOME': str(paths['config']), 'XDG_DATA_HOME': str(paths['data']),
        'XDG_CACHE_HOME': str(paths['cache']), 'XDG_STATE_HOME': str(paths['state']),
        'XDG_RUNTIME_DIR': str(paths['run']), 'TMPDIR': str(paths['tmp']),
        'TMP': str(paths['tmp']), 'TEMP': str(paths['tmp']),
        'LANG': 'C', 'LC_ALL': 'C', 'TZ': 'UTC',
        'PYTHONDONTWRITEBYTECODE': '1', 'PYTHONUTF8': '1',
    }
    for prefix in ('LOOPTENDER_', 'KEEPGOING_'):
        env[prefix + 'REGISTRY_DIR'] = str(paths['registry'])
        env[prefix + 'SESSION_DIR'] = str(paths['sessions'])
        env[prefix + 'ROUTES_FILE'] = str(root / 'routes.yaml')
    return env
