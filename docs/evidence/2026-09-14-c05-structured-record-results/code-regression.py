import json
import os
import subprocess
from pathlib import Path

root = Path('/Users/tiger/Projects/domainry-agent')
packages = json.loads(Path('/tmp/c05-record-results-executable-packages.json').read_text())['code_packages']
with Path('/tmp/c05-record-results-agent-final-code.log').open('w') as log:
    result = subprocess.run(['go', 'test', *packages, '-count=1', '-v'], cwd=root,
                            env=dict(os.environ, GOWORK=str(root / 'go.work')),
                            stdout=log, stderr=subprocess.STDOUT)
raise SystemExit(result.returncode)
