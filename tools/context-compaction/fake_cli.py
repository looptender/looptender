"""Only used by isolated tests; no provider or installed CLI is invoked."""
import json
import os
from pathlib import Path
import sys
import time

p = Path(os.environ['COMPACT_FIXTURE'])
w = json.loads(p.read_text())
# Model a new executable's one-time host startup cost, before it can answer.
time.sleep(w.pop('startupDelaySeconds', 0))
a = sys.argv[1:]
w.setdefault('calls', []).append(a)
p.write_text(json.dumps(w))
if a[0] == 'status': result = w['status']
elif a[:2] == ['assignment', 'inventory']: result = w['inventory']
elif a[:2] == ['thread', 'show']: result = w['parent']
elif a[:3] == ['thread', 'interactions', 'list']: result = []
elif a[:2] == ['thread', 'context']: result = {'usage': w['usage']}
elif a[:2] == ['thread', 'log']:
    after = int(a[a.index('--after-seq') + 1]); result = [e for e in w['rows'] if e['seq'] > after][:100]
elif a[:2] == ['thread', 'compact']:
    if w.get('lostReply'): sys.exit(2)
    result = {'ok': True, 'threadId': w['parent']['thread']['id']}
else: sys.exit(3)
print(json.dumps(result))
