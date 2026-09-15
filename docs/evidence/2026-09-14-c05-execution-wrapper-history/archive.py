import hashlib
import json
import re
import shutil
import subprocess
from datetime import datetime, timezone
from pathlib import Path

root = Path('/Users/tiger/Projects/domainry-agent')
parent = root / 'docs/evidence/2026-09-14-c05-structured-record-results'
out = root / 'docs/evidence/2026-09-14-c05-execution-wrapper-history'

def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()

def accepted(path):
    body = Path(path).read_text()
    assert re.search(r'^ok\s', body, re.M), path
    assert not re.search(r'^FAIL|^--- FAIL|WARNING: DATA RACE|panic:', body, re.M), path
    return body

logs = {
    'application-final-race.log': '/tmp/c05-execution-wrappers-application-bounds-final-race.log',
    'core-eight-final.log': '/tmp/c05-execution-wrappers-bounds-final-core.log',
    'real-product-final-normal.log': '/tmp/c05-execution-wrappers-product-direct-observer-final-normal.log',
    'before-bounds-and-observer-full-code.log': '/tmp/c05-execution-wrappers-agent-final-code.log',
    'previous-record-phase-full-code.log': '/tmp/c05-record-results-agent-final-code.log',
}
for path in logs.values():
    accepted(path)
normal = accepted(logs['real-product-final-normal.log'])
assert normal.count('original SHA-256 verified') == 45
assert normal.count('Exact original saved execution wrapper') == 18
assert normal.count('Exact original structured result') == 27
assert 'Actual second Identity account has read/list rights' in normal
assert 'TestSavedSharedExecutionWrapperRestoresOnlyItsExactEarlierFlattenedSource' in accepted(logs['application-final-race.log'])
assert 'TestSavedDelegationTaskResultSeparatesExecutionReadFromWriteAndCommunication' in accepted(logs['application-final-race.log'])

inventory = json.loads(Path('/tmp/c05-record-results-executable-packages.json').read_text())
for key in ['before-bounds-and-observer-full-code.log', 'previous-record-phase-full-code.log']:
    body = accepted(logs[key])
    results = re.findall(r'^(?:ok\s+|\?\s+)(github.com/domainry/domainry-agent/\S+)', body, re.M)
    assert len(results) == 36 and set(results) == set(inventory['code_packages'])
full_exit = json.loads(Path('/tmp/c05-execution-wrappers-agent-final-code-exit.json').read_text())
assert full_exit == {'exit_code': 0, 'packages': 36}

failed = {
    'diagnostic-product-allowlist.log': '/tmp/c05-execution-wrappers-product-first.log',
    'diagnostic-product-saved-source-denied.log': '/tmp/c05-execution-wrappers-product-second.log',
    'diagnostic-source-denial-trace.log': '/tmp/c05-execution-wrappers-product-trace.log',
    'diagnostic-old-flattened-source.log': '/tmp/c05-execution-wrappers-legacy-before-recovery.log',
    'diagnostic-original-task-concurrent-race-timeout.log': '/tmp/c05-execution-wrappers-product-final-race.log',
    'diagnostic-direct-observer-concurrent-race-timeout.log': '/tmp/c05-execution-wrappers-product-direct-observer-final-race.log',
    'diagnostic-ordinary-inspector-90s-observer.log': '/tmp/c05-execution-wrappers-product-serial-final-race.log',
    'diagnostic-original-recursive-all.log': '/tmp/c05-record-results-agent-final-all.log',
}
for path in failed.values():
    assert re.search(r'^FAIL|^--- FAIL', Path(path).read_text(), re.M), path
trace = Path(failed['diagnostic-source-denial-trace.log']).read_text()
assert 'tool=requirements_save scope=""' in trace and 'tool=delegation_get scope=""' in trace

pending = {}
race_path = '/tmp/c05-execution-wrappers-product-serial-300-final-race.log'
race_body = Path(race_path).read_text()
race = {'accepted': False, 'session_id': 13421}
if re.search(r'^ok\s', race_body, re.M) and not re.search(r'^FAIL|^--- FAIL|WARNING: DATA RACE|panic:', race_body, re.M):
    accepted(race_path)
    assert race_body.count('original SHA-256 verified') == 45
    logs['real-product-final-serial-race.log'] = race_path
    race.update(status='accepted', accepted=True, sha_checks=45)
elif re.search(r'^FAIL|^--- FAIL|WARNING: DATA RACE|panic:', race_body, re.M):
    failed['diagnostic-product-final-serial-race.log'] = race_path
    race.update(status='failed_not_accepted', sha_checks=race_body.count('original SHA-256 verified'))
else:
    rows = subprocess.run(['ps', '-axo', 'pid,ppid,etime,pcpu,args'], capture_output=True, text=True, check=True).stdout.splitlines()
    matches = [row for row in rows if len(row.split(None, 4)) == 5 and row.split(None, 4)[4].split()[0].endswith('/product.test') and '-test.cpuprofile=/tmp/c05-execution-wrappers-product-serial-300-cpu.pprof' in row]
    assert len(matches) == 1, matches
    process = matches[0]
    pid = int(process.split()[0])
    pending['real-product-serial-race-pending.log'] = race_path
    race.update(status='verified_running', pid=pid, process=process, sha_checks=race_body.count('original SHA-256 verified'))

launch = json.loads(Path('/tmp/c05-execution-wrappers-ordinary-budget-final-launch.json').read_text())['sources']
sources = {}
for path, digest in launch.items():
    if Path(path).suffix == '.go' or Path(path).name in ['go.mod', 'go.sum', 'go.work']:
        assert sha(path) == digest, path
    sources[path] = sha(path)
earlier = json.loads(Path('/tmp/c05-execution-wrappers-final-launch.json').read_text())['sources']
differences = {p: {'full_code_launch': h, 'current': sources[p]} for p, h in earlier.items() if Path(p).suffix == '.go' and h != sources[p]}
expected = [
    'internal/application/conversation_scoped_source_history.go',
    'internal/application/conversation_execution_tools_test.go',
    'internal/assembly/product/record_result_read_test.go',
]
assert set(differences) == {str(root / p) for p in expected}
previous_artifacts = json.loads((parent / 'artifact-sha256.json').read_text())
for name, digest in previous_artifacts.items():
    assert sha(parent / name) == digest, name
todo = (root / 'docs/agent-core-capabilities-todo.md').read_text()
assert len(re.findall(r'^- \[[ x]\] ', todo, re.M)) == 25
assert len(re.findall(r'^- \[x\] ', todo, re.M)) == 4

out.mkdir(parents=True, exist_ok=False)
for name, path in {**logs, **failed, **pending}.items():
    shutil.copyfile(path, out / name)
for name, path in {
    'agent-executable-packages.json': '/tmp/c05-record-results-executable-packages.json',
    'full-code-exit.json': '/tmp/c05-execution-wrappers-agent-final-code-exit.json',
    'full-code-launch.json': '/tmp/c05-execution-wrappers-final-launch.json',
    'record-phase-full-code-launch.json': '/tmp/c05-record-results-code-current-launch.json',
    'bounds-core-launch.json': '/tmp/c05-execution-wrappers-bounds-final-launch.json',
    'normal-90s-observer-launch.json': '/tmp/c05-execution-wrappers-direct-observer-final-launch.json',
    'final-ordinary-inspector-budget-launch.json': '/tmp/c05-execution-wrappers-ordinary-budget-final-launch.json',
    'diagnostic-90s-inspector-cpu.pprof': '/tmp/c05-execution-wrappers-product-serial-cpu.pprof',
    'diagnostic-90s-inspector-cpu-top.txt': '/tmp/c05-execution-wrappers-product-serial-cpu-top.txt',
    'diagnostic-90s-inspector-cpu-cumulative.txt': '/tmp/c05-execution-wrappers-product-serial-cpu-cumulative.txt',
    'trace-overlay.json': '/tmp/c05-execution-wrappers-trace/overlay.json',
    'trace-conversation_sources.go.txt': '/tmp/c05-execution-wrappers-trace/conversation_sources.go',
    'concurrent-web-native-sample.txt': '/tmp/c05-execution-wrappers-web-load-sample.txt',
    'concurrent-web-native-sample-resolved.json': '/tmp/c05-execution-wrappers-web-load-sample-resolved.json',
}.items():
    shutil.copyfile(path, out / name)
if race['status'] != 'verified_running':
    profile = Path('/tmp/c05-execution-wrappers-product-serial-300-cpu.pprof')
    if profile.exists():
        shutil.copyfile(profile, out / 'product-serial-cpu.pprof')
        result = subprocess.run(['go', 'tool', 'pprof', '-top', '-nodecount=30', str(profile)], capture_output=True, text=True, check=True)
        (out / 'product-serial-cpu-top.txt').write_text(result.stdout)
for name in ['source-sha256.json', 'artifact-sha256.json', 'acceptance.json']:
    shutil.copyfile(parent / name, out / ('previous-' + name))
for relative in [
    'internal/application/conversation_sources.go',
    'internal/application/conversation_execution_tools.go',
    'internal/application/conversation_scoped_source_history.go',
    'internal/application/conversation_execution_tools_test.go',
    'internal/assembly/product/record_result_read_test.go',
    'internal/assembly/product/record_inspector_model_test.go',
    'docs/testing-2026-09-14-c05-execution-wrapper-history.md',
    'docs/agent-core-capabilities-todo.md',
]:
    path = root / relative
    shutil.copyfile(path, out / (path.name + '.txt'))
(out / 'source-sha256.json').write_text(json.dumps(sources, indent=2) + '\n')
(out / 'source-version-differences.json').write_text(json.dumps(differences, indent=2) + '\n')
(out / 'commands.txt').write_text('''GOWORK=/Users/tiger/Projects/domainry-agent/go.work
go test -race ./internal/application -count=1 -v
go test ./internal/application ./internal/infrastructure/persistence/database/agent ./internal/infrastructure/provider ./internal/capability ./internal/transport/http/module ./remote ./server ./module -count=1 -v
go test ./internal/assembly/product -run '^TestProductStructuredOriginalRecordResultsUseRealIdentityReadRightsAndRestart$' -count=1 -timeout=5m -v
go test -race ./internal/assembly/product -run '^TestProductStructuredOriginalRecordResultsUseRealIdentityReadRightsAndRestart$' -count=1 -timeout=12m -cpuprofile=/tmp/c05-execution-wrappers-product-serial-300-cpu.pprof -o=/tmp/c05-execution-wrappers-product-serial-300.test -v
The full-code command uses every one of 36 code_packages in agent-executable-packages.json, -count=1 -timeout=20m -v; excludes only seven archived diagnostic snapshot directories.
The full-code regression compiled before the final invalid-source-boundary guard and observer-only fixture change. It is a versioned baseline, not a claim of every current source having been compiled in that command.
The final eight-core/Application race compile the bounds guard; actual Product normal uses the 90s inspector observer. The final serial Product race uses the service's unchanged ordinary five-minute inspector budget as its observation window. Its launch is recorded separately; production sources are identical.
Actual task budget remains 12 steps / 12 calls / 8192 output bytes / 60 seconds. Whole-package test observation limits do not alter task or model budgets.
''')
acceptance = {
    'phase_status': 'accepted_normal_scoped_execution_wrapper_history',
    'c05_complete': False, 'complete_todo_items': 4, 'total_todo_items': 25,
    'accepted_logs': list(logs), 'diagnostic_failed_logs': list(failed), 'pending_logs': list(pending),
    'actual_identity_accounts': 2, 'receiving_execution_steps': 6, 'original_receipts': 3,
    'actual_inspector_steps': 7, 'actual_inspector_tool_calls': 6,
    'inspector_has_no_professional_record_tools': True,
    'publication_explicitly_confirmed_through_actual_http': True,
    'normal_sha_checks': 45, 'normal_original_record_sha_checks': 27, 'normal_saved_wrapper_sha_checks': 18,
    'normal_fixture_version': 'normal-90s-observer-launch.json',
    'final_race_fixture_version': 'final-ordinary-inspector-budget-launch.json',
    'ordinary_inspector_service_budget_seconds': 300,
    'ordinary_inspector_fixture_observation_seconds': 300,
    'actual_delegated_saves': 1, 'sdk_fixture_saves': 2,
    'task_budget': {'steps': 12, 'calls': 12, 'output_bytes': 8192, 'timeout_seconds': 60},
    'write_revocation_read_revocation_restart_and_second_account_isolation_checked': True,
    'old_step_source_snapshot_not_rewritten': True,
    'earlier_receipt_exact_run_prefix_current_publication_current_read_and_communication_guards_checked': True,
    'actual_product_serial_race': race,
    'full_code_regression': {'exit_code': 0, 'packages': 36, 'version': 'before final bounds guard and direct observer fixture', 'launch': 'full-code-launch.json', 'differences': 'source-version-differences.json'},
    'previous_record_phase_code_regression': {'packages': 36, 'all_package_results_passed': True, 'launch': 'record-phase-full-code-launch.json'},
    'source_manifest': 'source-sha256.json', 'source_files': len(sources),
    'previous_artifacts_unchanged': len(previous_artifacts), 'previous_acceptance_sha256': sha(parent / 'acceptance.json'),
    'runtime_released_module_set_lock_changed': False,
    'remaining': ['Full C05 historical admission and execution source combinations', 'Broader query expressions and remaining source/dependency/Identity exit combinations', 'Earlier actual professional write race timeouts', 'Released Runtime module-set synchronization', 'C06 and all remaining TODO items'],
}
if not race['accepted']:
    acceptance['remaining'].insert(0, 'Actual shared execution wrapper Product race not accepted yet')
acceptance['created_at_utc'] = datetime.now(timezone.utc).isoformat()
(out / 'acceptance.json').write_text(json.dumps(acceptance, indent=2) + '\n')
shutil.copyfile(__file__, out / 'archive.py')
artifacts = {str(path.relative_to(out)): sha(path) for path in sorted(out.rglob('*')) if path.is_file()}
(out / 'artifact-sha256.json').write_text(json.dumps(artifacts, indent=2) + '\n')
print(json.dumps({'archive': str(out), 'sources': len(sources), 'artifacts': len(artifacts), 'accepted_logs': len(logs), 'race': race['status']}))
