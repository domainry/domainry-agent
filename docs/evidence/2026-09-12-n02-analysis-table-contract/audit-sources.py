"""Record this N02 increment without attributing concurrent owner changes."""
import hashlib
import json
import pathlib
import subprocess
from datetime import datetime, timezone

ROOT = pathlib.Path('/Users/tiger/Projects')
EVIDENCE = pathlib.Path(__file__).resolve().parent
REPOSITORIES = ['domainry-agent', 'domainry-agent-sdk', 'domainry-tools',
                'domainry-tools-sdk', 'domainry-runtime', 'domainry-report',
                'domainry-report-sdk', 'domainry-knowledge', 'domainry-connectors',
                'domainry-connector-sdk', 'anti/llm-proxy']


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def git(repository, *args):
    return subprocess.check_output(['git', *args], cwd=ROOT/repository, text=True).strip()


before = json.loads((EVIDENCE/'baseline.json').read_text())
owned = set(json.loads((EVIDENCE/'owned-paths.json').read_text()))
current = {}
for repository in REPOSITORIES:
    for path in (ROOT/repository).rglob('*'):
        if any(p in {'.git', 'node_modules', 'dist', 'build', '.cache', 'evidence'} for p in path.parts):
            continue
        if path.is_file() and (path.suffix in {'.go', '.md', '.json', '.ts', '.tsx'} or path.name in {'go.mod', 'go.sum', 'go.work'}):
            current[str(path)] = sha(path)
assert owned <= current.keys()

passed = ['checksum-final-race.log', 'report-complete-final.log',
          'sdk-complete-final-race.log', 'runtime-owner-final-race.log']
for name in passed:
    content = (EVIDENCE/name).read_text()
    assert 'ok ' in content and 'FAIL' not in content and 'DATA RACE' not in content, name
    assert '[no tests to run]' not in content, name
for name in ['report-final-vet.log', 'sdk-final-vet.log']:
    assert not (EVIDENCE/name).read_text().strip(), name
todo = (ROOT/'domainry-agent/docs/agent-capabilities-todo.md').read_text()
assert '- [x] N01：' in todo and '- [ ] N02：' in todo and '- [ ] N03：' in todo
assert '已完成 56 / 81' in todo
assert git('anti/llm-proxy', 'status', '--porcelain') == ''
web = (ROOT/'domainry-connectors/providers/web/llm_proxy/provider.go').read_text()
assert '"/tool/web_search"' in web and '"/tool/web_fetch_jina"' in web
assert 'chat/completions' not in web

changed, preserved, outside = [], [], []
for path, digest in sorted(current.items()):
    record = {'path': path, 'sha256': digest}
    if before.get(path) == digest:
        preserved.append(record)
    else:
        record['before_sha256'] = before.get(path)
        (changed if path in owned else outside).append(record)
deleted = sorted(set(before)-set(current))
assert not set(deleted) & owned
go_files = sorted(p for p in owned if p.endswith('.go'))
assert not subprocess.check_output(['gofmt', '-l', *go_files], text=True).strip()
for repository in ['domainry-agent', 'domainry-report', 'domainry-report-sdk']:
    result = subprocess.run(['git', 'diff', '--check'], cwd=ROOT/repository, capture_output=True, text=True)
    assert result.returncode == 0, result.stdout+result.stderr

artifacts = [{'path': str(p), 'sha256': sha(p)} for p in sorted(EVIDENCE.rglob('*'))
             if p.is_file() and p.name != 'audit-validation.log']
manifest = {
    'stage': 'N02 independent complete structured table source contract and Report owner',
    'captured_at': datetime.now(timezone.utc).isoformat(),
    'todo_complete': False,
    'remaining': ['Actual Knowledge/data-service complete-table API or full aggregation source',
                  'Concrete source adapter and file HTTP/product/browser acceptance',
                  'N03 is not started'],
    'scope': 'No file parser, original download, Knowledge cache/index, upstream service, frontend or model changes. Source streaming and original-file analysis are not treated as the same acceptance.',
    'source_contract': 'AnalysisTableHost -> AnalysisTableSource, separately selected from ObjectSQLExecutor',
    'proof': 'Complete EOF, declared versus delivered row count, full projected-cell SHA-256, current subject/field authorization and before/after definition/data version',
    'workspace': '/tmp/domainry-n01.go.work',
    'baseline_files': len(before), 'baseline_sha256': sha(EVIDENCE/'baseline.json'),
    'edited_paths': sorted(owned), 'changed_sources': changed, 'preserved_sources': preserved,
    'outside_increment_changes': outside, 'outside_increment_deletions': deleted,
    'attribution': 'Only explicit edited paths belong to this increment. Other changes are recorded without reversal or ownership claims. Whole-file hashes also include any previously present code. Identity duplicate-import collision is described in execution-notes.json and has no claimed final edit.',
    'heads': {repo: git(repo, 'rev-parse', 'HEAD') for repo in REPOSITORIES if (ROOT/repo/'.git').exists()},
    'passed_logs': passed,
    'test_inputs': {'domain_rows': 1205, 'public_module_structured_rows': 1501,
                    'public_module_sum': '9007199254741008.25',
                    'actual_runtime_authorized_rows': 1203, 'actual_runtime_matching_rows': 1201},
    'limits': {'input_rows': 100000, 'input_budget_bytes': 16777216,
               'state_budget_bytes': 16777216, 'cell_bytes': 16384, 'output_rows': 500},
    'not_claimed': ['Actual full Excel file/service or product/browser acceptance',
                    'Real model decisions', 'Whole Runtime suite or immutable dependency release gate',
                    'Identity concurrent workload implementation'],
    'llm_proxy': {'clean': True, 'head': git('anti/llm-proxy', 'rev-parse', 'HEAD'),
                  'scope': ['POST /tool/web_search', 'POST /tool/web_fetch_jina']},
    'evidence': artifacts,
}
output = EVIDENCE.parent/'2026-09-12-n02-analysis-table-contract.json'
output.write_text(json.dumps(manifest, ensure_ascii=False, indent=2)+'\n')
for group in ['changed_sources', 'preserved_sources', 'outside_increment_changes', 'evidence']:
    for item in manifest[group]:
        assert sha(pathlib.Path(item['path'])) == item['sha256'], item['path']
print(json.dumps({'increment_changed': len(changed), 'preserved': len(preserved),
                  'outside_increment_changed': len(outside), 'deleted': len(deleted),
                  'evidence': len(artifacts), 'verified_hashes': len(current)+len(artifacts)}, ensure_ascii=False))
