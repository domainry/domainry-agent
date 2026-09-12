"""Audit only this increment's edits; keep concurrent workspace changes separate."""
import hashlib
import json
import pathlib
import subprocess
from datetime import datetime, timezone

ROOT = pathlib.Path('/Users/tiger/Projects')
EVIDENCE = pathlib.Path(__file__).resolve().parent
OUTPUT = EVIDENCE.parent / '2026-09-12-n02-analysis-tools.json'


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def git(name, *args):
    return subprocess.check_output(['git', *args], cwd=ROOT / name, text=True).strip()


before = json.loads((EVIDENCE / 'baseline.json').read_text())
owned = set(json.loads((EVIDENCE / 'owned-paths.json').read_text()))
roots = sorted({pathlib.Path(p).relative_to(ROOT).parts[0] for p in before})
current = {}
for name in roots:
    for path in (ROOT / name).rglob('*'):
        if any(p in {'.git', 'node_modules', 'dist', 'build', '.cache', 'evidence'} for p in path.parts):
            continue
        if path.is_file() and (path.suffix in {'.go', '.md', '.json', '.ts', '.tsx'} or path.name in {'go.mod', 'go.sum', 'go.work'}):
            current[str(path)] = sha(path)

assert owned <= set(current), sorted(owned - set(current))
browser = json.loads((EVIDENCE / 'browser-complete/result.json').read_text())
assert browser['passed'] and not browser['pageErrors']
assert browser['mobile'] == {'width': 390, 'scroll': 390}
assert browser['revokedFullResult']['status'] != 200
assert git('anti/llm-proxy', 'status', '--porcelain') == ''
todo = (ROOT / 'domainry-agent/docs/agent-capabilities-todo.md').read_text()
assert '- [x] N01：' in todo and '- [ ] N02：' in todo and '- [ ] N03：' in todo
assert '已完成 56 / 81' in todo

passed_logs = [
    'agent-final-full.log', 'agent-sdk-verified-race.log', 'tools-full-final-race.log',
    'tools-sdk-race.log', 'runtime-rpc-final-race.log', 'runtime-owner-final-race.log',
    'conversation-http-final.log', 'result-http-final-race.log', 'runtime-modified-packages-race.log',
    'runtime-source-boundary-final.log', 'browser-complete-host.log', 'work-full.log', 'pm-full.log',
]
for name in passed_logs:
    text = (EVIDENCE / name).read_text()
    assert 'ok ' in text or '? ' in text, name
    assert 'FAIL' not in text and 'DATA RACE' not in text and '[no tests to run]' not in text, name

changed, outside, unchanged = [], [], []
for path, digest in sorted(current.items()):
    entry = {'path': path, 'sha256': digest}
    if before.get(path) == digest:
        unchanged.append(entry)
    else:
        entry['before_sha256'] = before.get(path)
        (changed if path in owned else outside).append(entry)
deleted = sorted(set(before) - set(current))
assert not set(deleted) & owned

# This file restores precisely the pre-increment seed decoder for the two
# independent workspace failures. It is archived as text, never compiled.
seed = ROOT / 'domainry-runtime/runtime/application/seed/business/records.go'
assert sha(EVIDENCE / 'seed-before/records.go.txt') == before[str(seed)]
assert 'TestRuntimeSeedSynchronizationBindsInstallationWorkspace' in (EVIDENCE / 'seed-existing-failures-baseline.log').read_text()
assert 'TestSynchronizeRuntimeSeedsDoesNotRequireIdentityProjection' in (EVIDENCE / 'seed-existing-failures-baseline.log').read_text()

formatting = []
for name in roots:
    files = [p for p in owned if p.endswith('.go') and pathlib.Path(p).relative_to(ROOT).parts[0] == name]
    if files:
        output = subprocess.check_output(['gofmt', '-l', *sorted(files)], text=True)
        assert not output.strip(), output
    if (ROOT / name / '.git').exists():
        result = subprocess.run(['git', 'diff', '--check'], cwd=ROOT / name, capture_output=True, text=True)
        assert result.returncode == 0, result.stdout + result.stderr
        formatting.append({'repository': name, 'git_diff_check_exit': result.returncode})

artifacts = [{'path': str(p), 'sha256': sha(p)} for p in sorted(EVIDENCE.rglob('*')) if p.is_file() and p.name != 'audit-validation.log']
audit = {
    'stage': 'N02 analysis tools / host RPC / product composition and generic complete-result browser reads',
    'captured_at': datetime.now(timezone.utc).isoformat(),
    'todo_complete': False,
    'remaining': ['N02 complete table-file datasets and their full permission/product acceptance', 'N03 is not started'],
    'workspace': '/tmp/domainry-n01.go.work',
    'baseline_files': len(before),
    'baseline_sha256': sha(EVIDENCE / 'baseline.json'),
    'edited_paths': sorted(owned),
    'changed_sources': changed,
    'preserved_sources': unchanged,
    'outside_increment_changes': outside,
    'outside_increment_deletions': deleted,
    'attribution': 'Explicit edited paths only. Other workspace changes are captured separately and neither reverted nor claimed as this increment. Hashes record whole current files, not ownership of every pre-existing or concurrent hunk.',
    'heads': {name: git(name, 'rev-parse', 'HEAD') for name in roots if (ROOT / name / '.git').exists()},
    'without_git_metadata': [name for name in roots if not (ROOT / name / '.git').exists()],
    'model_scope': 'In-process deterministic SDK decisions using real owner responses; no model-vendor HTTP request',
    'conversation_modes': ['Runtime-embedded Agent', 'separate Agent product database via Runtime businessrpc HTTP, same test process and shared Identity'],
    'business_contract_sha256': '48ec2f8f8211ab88c34365d0663cdc75b1ed60ae4774510d8f69c28bcf69a038',
    'agent_public_http_routes': 74,
    'passed_logs': passed_logs,
    'browser': {'phases': browser['phases'], 'mobile': browser['mobile'], 'page_errors': browser['pageErrors'], 'revoked_full_result': browser['revokedFullResult']},
    'known_unpassed_gates': [
        {'todo': 'H04', 'test': 'TestRuntimeConsumesDomainryModulesByImmutableVersion', 'reason': 'Tools / Tools SDK remain v0.0.0 with no immutable release; prior N01 failure retained, gate unchanged'},
        {'tests': ['TestRuntimeSeedSynchronizationBindsInstallationWorkspace', 'TestSynchronizeRuntimeSeedsDoesNotRequireIdentityProjection'], 'scope': 'Two bootstrap workspace failures also reproduced with exact pre-increment seed decoder using Go overlay; no whole Runtime bootstrap pass claimed', 'log': 'seed-existing-failures-baseline.log'},
    ],
    'formatting': formatting,
    'llm_proxy': {'head': git('anti/llm-proxy', 'rev-parse', 'HEAD'), 'clean': True, 'scope': ['POST /tool/web_search', 'POST /tool/web_fetch_jina']},
    'evidence': artifacts,
}
OUTPUT.write_text(json.dumps(audit, indent=2, ensure_ascii=False) + '\n')
for group in ('changed_sources', 'preserved_sources', 'outside_increment_changes', 'evidence'):
    for item in audit[group]:
        assert sha(pathlib.Path(item['path'])) == item['sha256'], item['path']
print(json.dumps({'increment_changed': len(changed), 'preserved': len(unchanged), 'outside_increment_changed': len(outside), 'outside_increment_deleted': len(deleted), 'evidence': len(artifacts), 'verified_hashes': len(current) + len(artifacts)}, ensure_ascii=False))
