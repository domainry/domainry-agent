import hashlib
import json
import re
import shutil
import subprocess
from datetime import datetime, timezone
from pathlib import Path

root = Path('/Users/tiger/Projects/domainry-agent')
parent = root / 'docs/evidence/2026-09-14-c05-query-exclusions-cursor'
out = root / 'docs/evidence/2026-09-14-c05-structured-record-results'

def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()

def accepted(path):
    body = Path(path).read_text()
    assert re.search(r'^ok\s', body, re.M), path
    assert not re.search(r'^FAIL|^--- FAIL|WARNING: DATA RACE|panic:', body, re.M), path
    return body

logs = {
    'tools-all.log': '/tmp/c05-record-results-tools-final-all.log',
    'tools-result-policy-race.log': '/tmp/c05-record-results-tools-final-race.log',
    'agent-application-race.log': '/tmp/c05-record-results-application-final-race.log',
    'real-product-normal.log': '/tmp/c05-record-results-product-final-normal.log',
    'artifact-source-cutoff.log': '/tmp/c05-record-results-artifact-cutoff-after-fixture-meter.log',
}
for path in logs.values():
    accepted(path)
normal = accepted(logs['real-product-normal.log'])
assert normal.count('original SHA-256 verified') == 27
assert 'Actual second Identity account has read/list rights' in normal
pending = {}
race_path = '/tmp/c05-record-results-product-separated-windows-race.log'
race = Path(race_path).read_text()
if re.search(r'^ok\s', race, re.M):
    accepted(race_path)
    assert race.count('original SHA-256 verified') == 27
    logs['real-product-race.log'] = race_path
    race_state = {'status': 'accepted', 'sha_checks': 27}
else:
    assert not re.search(r'^FAIL|^--- FAIL|WARNING: DATA RACE', race, re.M)
    process = subprocess.run(['ps', '-p', '94326', '-o', 'pid,etime,pcpu,state,args'], capture_output=True, text=True, check=True).stdout
    assert 'product.test' in process and 'TestProductStructuredOriginalRecordResultsUseRealIdentityReadRightsAndRestart' in process
    pending['real-product-race-pending.log'] = race_path
    race_state = {'status': 'verified_running', 'pid': 94326, 'session_id': 16706, 'process': process, 'completed_sha_checks': race.count('original SHA-256 verified')}

failed = {
    'diagnostic-record-policy-before-fix.log': '/tmp/c05-record-results-before-fix.log',
    'diagnostic-product-profile-required-fields.log': '/tmp/c05-record-results-real-product-first.log',
    'diagnostic-stale-acceptance-revision.log': '/tmp/c05-record-results-real-agent-first.log',
    'diagnostic-session-selected-role.log': '/tmp/c05-record-results-product-shared-diagnostic.log',
    'diagnostic-own-execution-scope.log': '/tmp/c05-record-results-product-shared-second.log',
    'diagnostic-own-scope-unit-before-fix.log': '/tmp/c05-record-results-own-scope-before-fix.log',
    'diagnostic-combined-observer-race.log': '/tmp/c05-record-results-product-final-race.log',
}
for path in failed.values():
    assert re.search(r'^FAIL|^--- FAIL', Path(path).read_text(), re.M), path

recursive_path = '/tmp/c05-record-results-agent-final-all.log'
recursive_body = Path(recursive_path).read_text()
recursive_process = subprocess.run(['ps', '-p', '93415', '-o', 'pid,etime,pcpu,state,args'], capture_output=True, text=True)
if recursive_process.returncode == 0:
    assert 'go test ./... -count=1' in recursive_process.stdout
    pending['recursive-all-running-with-diagnostics.log'] = recursive_path
    recursive_state = {'status': 'verified_running_with_setup_and_integration_failures', 'accepted': False, 'pid': 93415, 'session_id': 34284, 'process': recursive_process.stdout}
else:
    assert re.search(r'^FAIL$', recursive_body, re.M)
    failed['diagnostic-recursive-all-and-old-fixture.log'] = recursive_path
    recursive_state = {'status': 'failed_not_accepted', 'accepted': False}

code_path = Path('/tmp/c05-record-results-agent-final-code.log')
code_state = {'status': 'not_started', 'accepted': False}
if code_path.exists():
    code = code_path.read_text()
    if re.search(r'^ok\s.*internal/assembly/web', code, re.M) and not re.search(r'^FAIL|^--- FAIL', code, re.M):
        accepted(code_path)
        inventory = json.loads(Path('/tmp/c05-record-results-executable-packages.json').read_text())
        terminal_packages = set(re.findall(r'^(?:ok\s+|\?\s+)(\S+)', code, re.M))
        assert terminal_packages == set(inventory['code_packages']), terminal_packages
        logs['agent-executable-code-all.log'] = str(code_path)
        code_state = {'status': 'accepted', 'accepted': True, 'packages': len(terminal_packages)}
    else:
        raise AssertionError('code regression must be terminal before this archive')

tools_launch = json.loads(Path('/tmp/c05-record-results-final-launch.json').read_text())['sources']
for path, digest in tools_launch.items():
    if '/domainry-tools/' in path:
        assert sha(path) == digest, path
launch = json.loads(Path('/tmp/c05-record-results-final-current-launch.json').read_text())['sources']
for path, digest in launch.items():
    if Path(path).suffix == '.go' or Path(path).name in ['go.mod', 'go.sum', 'go.work']:
        assert sha(path) == digest, path
previous_artifacts = json.loads((parent / 'artifact-sha256.json').read_text())
for name, digest in previous_artifacts.items():
    assert sha(parent / name) == digest, name
todo = (root / 'docs/agent-core-capabilities-todo.md').read_text()
assert len(re.findall(r'^- \[[ x]\] ', todo, re.M)) == 25
assert len(re.findall(r'^- \[x\] ', todo, re.M)) == 4
sources = set(launch)
sources.add(str(root / 'docs/testing-2026-09-14-c05-structured-record-results.md'))
sources = {path: sha(path) for path in sorted(sources)}
out.mkdir(parents=True, exist_ok=False)
for name, path in {**logs, **failed, **pending}.items():
    shutil.copyfile(path, out / name)
shutil.copyfile(__file__, out / 'archive.py')
for name, path in {
    'tools-launch-sources.json': '/tmp/c05-record-results-final-launch.json',
    'original-all-launch-sources.json': '/tmp/c05-record-results-final-agent-launch.json',
    'separated-race-launch-sources.json': '/tmp/c05-record-results-separated-windows-launch.json',
    'final-current-launch-sources.json': '/tmp/c05-record-results-final-current-launch.json',
    'agent-executable-packages.json': '/tmp/c05-record-results-executable-packages.json',
    'code-regression.py': '/tmp/c05-record-results-code-regression.py',
    'agent-go.work': str(root / 'go.work'),
}.items():
    shutil.copyfile(path, out / name)
for name in ['source-sha256.json', 'artifact-sha256.json', 'acceptance.json']:
    shutil.copyfile(parent / name, out / ('previous-' + name))
snapshots = [root / 'internal/application/conversation_shared_sources.go', root / 'internal/application/conversation_shared_sources_test.go', root / 'internal/assembly/product/record_result_read_test.go', root / 'internal/assembly/product/record_delivery_model_test.go', root / 'integration/conversation_sources_integration_test.go', root.parent / 'domainry-tools/internal/adapter/recordtools/adapter.go', root.parent / 'domainry-tools/internal/adapter/recordtools/result_read.go', root.parent / 'domainry-tools/internal/adapter/recordtools/result_read_test.go']
for path in snapshots:
    shutil.copyfile(path, out / (path.name + '.txt'))
(out / 'source-sha256.json').write_text(json.dumps(sources, ensure_ascii=False, indent=2) + '\n')
(out / 'commands.txt').write_text('''GOWORK=/Users/tiger/Projects/domainry-agent/go.work

Tools:
go test ./... -count=1
go test -race ./internal/adapter/recordtools ./internal/application/tool -count=1 -v

Agent:
go test -race ./internal/application -count=1
go test ./internal/assembly/product -run '^TestProductStructuredOriginalRecordResultsUseRealIdentityReadRightsAndRestart$' -count=1 -v
go test -race ./internal/assembly/product -run '^TestProductStructuredOriginalRecordResultsUseRealIdentityReadRightsAndRestart$' -count=1 -v
go test ./integration -run '^TestArtifactSourcesStopAtTheGeneratingStep$' -count=1 -v

Diagnostic recursive command: go test ./... -count=1
It includes archived standalone diagnostic .go files and is not accepted.
The full executable package inventory is agent-executable-packages.json (36 code packages).
The prepared code-regression.py executes every listed code package; it excludes only 7 immutable historical evidence package directories and never rewrites them.
Dispatch and receiving execution have separate fixture observation windows. Actual task budget remains 12 steps / 12 calls / 8192 bytes / 60 seconds.
The sourceModel fixture uses the production chat_completions encoder for input sizing; no execution context or budget was enlarged.
The published Runtime module-set lock remains unchanged and full bootstrap is not accepted.
''')
acceptance = {
    'phase_status': 'accepted_structured_record_read_policy_and_actual_personal_agent_delivery',
    'c05_complete': False, 'complete_todo_items': 4, 'total_todo_items': 25,
    'accepted_logs': list(logs), 'accepted_exit_codes': {name: 0 for name in logs},
    'diagnostic_failed_logs': list(failed), 'pending_logs': list(pending),
    'actual_identity_accounts': 2, 'actual_independent_agent_execution_steps': 6,
    'actual_original_receipts': 3, 'actual_normal_sha_checks': 27,
    'actual_page_paths': ['current delivery', 'exact original historical delivery', 'explicitly shared execution'],
    'actual_delegated_save_effects': 1, 'sdk_fixture_save_effects': 2,
    'actual_task_budget': {'steps': 12, 'calls': 12, 'output_bytes': 8192, 'timeout_seconds': 60},
    'actual_write_revocation_read_revocation_restart_and_owner_isolation_checked': True,
    'shared_execution_published_by_actual_owner_http_not_agent_tool_in_this_fixture': True,
    'real_product_race': race_state, 'agent_executable_code_regression': code_state,
    'recursive_go_test_all_accepted': False,
    'recursive_go_test_all': recursive_state,
    'historical_evidence_snapshots_not_rewritten': True,
    'original_effect_version_cursor_and_large_integer_preserved': True,
    'reader_withdrawal_during_original_receipt_io_unit_checked': True,
    'single_host_migration_ledger_checked': True,
    'source_manifest': 'source-sha256.json', 'source_files': len(sources),
    'artifact_manifest': 'artifact-sha256.json', 'previous_artifacts_unchanged': len(previous_artifacts),
    'previous_acceptance_sha256': sha(parent / 'acceptance.json'),
    'record_personal_ownership_not_cross_user_shared': True,
    'runtime_published_module_set_lock_modified': False,
    'runtime_bootstrap_complete_package_accepted': False,
    'observed_at_utc': datetime.now(timezone.utc).isoformat(),
    'remaining': ['broader business query expression scopes', 'other multiple-owner old roles and mixed roots', 'complete historical admission and execution-source receipts', 'other dependency management', 'broader Identity subject exit linkage', 'published Runtime module-set synchronization', 'earlier actual professional write race timeouts', 'all later TODO items retained in original order'],
}
(out / 'acceptance.json').write_text(json.dumps(acceptance, ensure_ascii=False, indent=2) + '\n')
artifacts = {str(path.relative_to(out)): sha(path) for path in sorted(out.rglob('*')) if path.is_file()}
(out / 'artifact-sha256.json').write_text(json.dumps(artifacts, ensure_ascii=False, indent=2) + '\n')
for path, digest in sources.items():
    assert sha(path) == digest, path
for name, digest in artifacts.items():
    assert sha(out / name) == digest, name
for name, digest in previous_artifacts.items():
    assert sha(parent / name) == digest, name
print(json.dumps({'sources': len(sources), 'artifacts': len(artifacts), 'previous_artifacts_unchanged': len(previous_artifacts), 'real_product_race': race_state, 'code_regression': code_state}, ensure_ascii=False))
