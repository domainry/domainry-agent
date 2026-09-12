"""Record this increment without rewriting the earlier Report-owner snapshot."""
import hashlib
import json
import pathlib
import subprocess
from datetime import datetime, timezone

ROOT = pathlib.Path('/Users/tiger/Projects')
EVIDENCE = pathlib.Path(__file__).resolve().parent
BASELINE = pathlib.Path('/tmp/n01-tools-before.json')
OUTPUT = EVIDENCE.parent / '2026-09-12-n01-report-tools.json'


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def git(name, *args):
    return subprocess.check_output(['git', *args], cwd=ROOT / name, text=True).strip()


before = json.loads(BASELINE.read_text())
roots = sorted({pathlib.Path(p).relative_to(ROOT).parts[0] for p in before})
current = {}
for name in roots:
    for path in (ROOT / name).rglob('*'):
        if any(p in {'.git', 'node_modules', 'dist', 'build', '.cache', 'evidence'} for p in path.parts):
            continue
        if path.is_file() and (path.suffix in {'.go', '.md', '.json', '.ts', '.tsx'} or path.name in {'go.mod', 'go.sum', 'go.work'}):
            current[str(path)] = sha(path)

browser = json.loads((EVIDENCE / 'browser-final/result.json').read_text())
assert browser['passed']
assert not browser['pageErrors']
assert 'PASS' in (EVIDENCE / 'conversation-module-race-optimized.log').read_text()
assert 'DATA RACE' not in (EVIDENCE / 'conversation-module-race-optimized.log').read_text()
assert git('anti/llm-proxy', 'status', '--porcelain') == ''
assert '- [x] N01：' in (ROOT / 'domainry-agent/docs/agent-capabilities-todo.md').read_text()
assert '- [ ] N02：' in (ROOT / 'domainry-agent/docs/agent-capabilities-todo.md').read_text()

changed = [{'path': p, 'sha256': h, 'before_sha256': before.get(p)} for p, h in sorted(current.items()) if before.get(p) != h]
unchanged = [{'path': p, 'sha256': h} for p, h in sorted(current.items()) if before.get(p) == h]
deleted = sorted(set(before) - set(current))
assert not deleted, deleted
artifacts = [{'path': str(p), 'sha256': sha(p)} for p in sorted(EVIDENCE.rglob('*')) if p.is_file() and p.name != 'audit-validation.log']
audit = {
    'stage': 'N01 Tools / Agent / Runtime / product composition and browser acceptance',
    'captured_at': datetime.now(timezone.utc).isoformat(),
    'todo_complete': True,
    'next': 'N02, in TODO order; N02 has not been implemented in this increment',
    'workspace': '/tmp/domainry-n01.go.work',
    'baseline_sha256': sha(BASELINE),
    'baseline_files': len(before),
    'heads': {name: git(name, 'rev-parse', 'HEAD') for name in roots if (ROOT / name / '.git').exists()},
    'without_git_metadata': [name for name in roots if not (ROOT / name / '.git').exists()],
    'model_scope': 'In-process deterministic SDK fixture; no model vendor HTTP call in this increment',
    'real_components': ['Report', 'Tools', 'Agent', 'Identity', 'Runtime records/actions', 'SQLite', 'Chrome'],
    'conversation_modes': ['embedded Runtime Agent', 'separate Agent product database using public Runtime businessrpc HTTP in the same test process'],
    'business_contract_sha256': '0e804a1dd71ff7ac10663bea843956d50927500fc41c3c39d808da09958ec2a5',
    'unpassed_gate': {'todo': 'H04', 'test': 'TestRuntimeConsumesDomainryModulesByImmutableVersion', 'reason': 'Tools and Tools SDK have no published immutable version; Runtime requires v0.0.0 in local workspace development', 'log': 'runtime-boundary-race.log', 'test_rule_unchanged': True},
    'architecture_separate_run': "go test -count=1 -race ./runtime/boundary -skip '^TestRuntimeConsumesDomainryModulesByImmutableVersion$'",
    'llm_proxy': {'head': git('anti/llm-proxy', 'rev-parse', 'HEAD'), 'clean': True, 'scope': ['POST /tool/web_search', 'POST /tool/web_fetch_jina']},
    'browser': {'passed_phases': browser['phases'], 'mobile': browser['mobile'], 'page_errors': browser['pageErrors'], 'race_browser_not_run': 'The race host hit the original observer deadline before browser startup; subsequent race acceptance runs the complete module HTTP scenario without Chrome.'},
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
