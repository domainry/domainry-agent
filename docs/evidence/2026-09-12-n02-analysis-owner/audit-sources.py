"""Freeze N02 owner evidence without modifying earlier TODO snapshots."""
import hashlib
import json
import pathlib
import subprocess
from datetime import datetime, timezone

ROOT = pathlib.Path('/Users/tiger/Projects')
EVIDENCE = pathlib.Path(__file__).resolve().parent
BASELINE = EVIDENCE / 'baseline.json'
OUTPUT = EVIDENCE.parent / '2026-09-12-n02-analysis-owner.json'

def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()

def git(name, *args):
    return subprocess.check_output(['git', *args], cwd=ROOT / name, text=True).strip()

before = json.loads(BASELINE.read_text())
assert len(before) == 3041
roots = sorted({pathlib.Path(p).relative_to(ROOT).parts[0] for p in before})
current = {}
for name in roots:
    for path in (ROOT / name).rglob('*'):
        if any(p in {'.git', 'node_modules', 'dist', 'build', '.cache', 'evidence'} for p in path.parts):
            continue
        if path.is_file() and (path.suffix in {'.go', '.md', '.json', '.ts', '.tsx'} or path.name in {'go.mod', 'go.sum', 'go.work'}):
            current[str(path)] = sha(path)

required = ['report-final-race.log', 'sdk-race.log', 'runtime-related-race.log',
            'runtime-storage-final-race.log', 'runtime-analysis-final-race.log',
            'runtime-report-boundary.log']
for name in required:
    content = (EVIDENCE / name).read_text()
    assert 'ok ' in content and 'FAIL' not in content and 'DATA RACE' not in content, name
for name in ['report-vet.log', 'sdk-vet.log', 'runtime-vet.log', 'runtime-final-vet.log']:
    assert (EVIDENCE / name).exists() and not (EVIDENCE / name).read_text().strip(), name
assert git('anti/llm-proxy', 'status', '--porcelain') == ''
assert '- [x] N01：' in (ROOT / 'domainry-agent/docs/agent-capabilities-todo.md').read_text()
assert '- [ ] N02：' in (ROOT / 'domainry-agent/docs/agent-capabilities-todo.md').read_text()
assert '- [ ] N03：' in (ROOT / 'domainry-agent/docs/agent-capabilities-todo.md').read_text()
dependencies = (EVIDENCE / 'report-dependencies.txt').read_text().splitlines()
for prefix in ['github.com/domainry/domainry-runtime', 'github.com/domainry/domainry-agent', 'github.com/domainry/domainry-tools']:
    assert not any(d.startswith(prefix) for d in dependencies), prefix

changed = [{'path': p, 'sha256': h, 'before_sha256': before.get(p)} for p, h in sorted(current.items()) if before.get(p) != h]
unchanged = [{'path': p, 'sha256': h} for p, h in sorted(current.items()) if before.get(p) == h]
deleted = sorted(set(before) - set(current))
assert not deleted, deleted
artifacts = [{'path': str(p), 'sha256': sha(p)} for p in sorted(EVIDENCE.rglob('*')) if p.is_file() and p.name != 'audit-validation.log']
audit = {
    'stage': 'N02 optional Report SDK, private Report owner and Runtime data-host increment',
    'captured_at': datetime.now(timezone.utc).isoformat(),
    'todo_complete': False,
    'completed_todos': 56,
    'total_todos': 81,
    'next': 'Continue N02: Tools, Agent/product/HTTP/browser and authorized complete table-file datasets. Do not enter N03.',
    'workspace': '/tmp/domainry-n01.go.work',
    'baseline_sha256': sha(BASELINE),
    'baseline_files': len(before),
    'heads': {name: git(name, 'rev-parse', 'HEAD') for name in roots if (ROOT / name / '.git').exists()},
    'without_git_metadata': [name for name in roots if not (ROOT / name / '.git').exists()],
    'real_components': ['Report public factory and private owner', 'Runtime RecordApplicationService and policies', 'RecordStore', 'ReportSQLStore', 'SQLite', 'host migration ledger'],
    'identity_scope': 'Identity SDK AccessBundle fixture; no Identity HTTP login or token issuer service tested in this increment',
    'model_scope': 'No model invoked',
    'browser_scope': 'Not implemented or tested for N02 yet',
    'database_scope': {'engine': 'sqlite', 'seeded': 1205, 'authorized': 1203, 'matching': 1201, 'other_user_and_workspace_excluded': 2, 'large_integer': '9007199254740993.01', 'division_result': '3002399751580331.003333'},
    'restart_scope': 'Report owner reopened on the same host database, not a full Runtime or Identity restart',
    'tested': required,
    'full_runtime_release_gate': 'Not run in this increment. N01 recorded immutable Tools and Tools SDK versions unavailable; remains H04. Gate not weakened.',
    'llm_proxy': {'head': git('anti/llm-proxy', 'rev-parse', 'HEAD'), 'clean': True, 'scope': ['POST /tool/web_search', 'POST /tool/web_fetch_jina']},
    'changed_sources': changed,
    'preserved_sources': unchanged,
    'deleted_sources': deleted,
    'evidence': artifacts,
}
OUTPUT.write_text(json.dumps(audit, indent=2, ensure_ascii=False) + '\n')
for group in ('changed_sources', 'preserved_sources', 'evidence'):
    for item in audit[group]:
        assert sha(pathlib.Path(item['path'])) == item['sha256'], item['path']
print(json.dumps({'changed': len(changed), 'preserved': len(unchanged), 'evidence': len(artifacts), 'verified_hashes': len(changed) + len(unchanged) + len(artifacts), 'deleted': len(deleted)}, ensure_ascii=False))
