import hashlib
import json
import re
import shutil
import subprocess
from datetime import datetime, timezone
from pathlib import Path

root = Path('/Users/tiger/Projects/domainry-agent')
runtime = root.parent / 'domainry-runtime'
parent = root / 'docs/evidence/2026-09-14-c05-knowledge-other-sources'
out = root / 'docs/evidence/2026-09-14-c05-query-exclusions-cursor'

def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()

def successful(path):
    body = Path(path).read_text()
    assert re.search(r'^ok\s', body, re.M), path
    assert not re.search(r'^FAIL|^--- FAIL|WARNING: DATA RACE|panic:', body, re.M), path
    return body

logs = {
    'runtime-scope-owners.log': '/tmp/c05-business-query-complete-scope-owners.log',
    'runtime-scope-race.log': '/tmp/c05-business-query-complete-scope-race.log',
    'real-current-query-and-business-rpc.log': '/tmp/c05-business-query-current-scope-real.log',
    'real-intermediate-three-business-roots.log': '/tmp/c05-business-query-complex-final-real.log',
    'real-managed-knowledge-race-20m.log': '/tmp/c05-knowledge-final-managed-race-20m.log',
}
for path in logs.values():
    successful(path)
query_root = 'TestCrossUserBusinessAgentComplexFiltersOriginalCursorAutomaticDeliveryAndAcceptance'
rpc_root = 'TestCrossUserBusinessOriginalReceiptsThroughRealOwnerRPCAndRestart'
normal = successful(logs['real-current-query-and-business-rpc.log'])
passed = re.findall(r'^--- PASS: (\S+) \(([0-9.]+)s\)', normal, re.M)
assert [name for name, _ in passed] == [query_root, rpc_root], passed
assert normal.count('original SHA-256 verified') == 20
assert 'execution-authorized reader\'s own query accepted and foreign producer cursor rejected' in normal
scope = successful(logs['runtime-scope-race.log'])
for name in ['explicit_negations', 'native_exclusions', 'native_producer_and_explicit_reader', 'explicit_producer_and_native_reader']:
    assert '--- PASS: TestSharedBusinessNegatedScopesReadOriginalORMPagesWithoutForeignCursorAuthority/'+name in scope, name
managed_root = 'TestCrossUserManagedKnowledgeAgentDispatchSharedOriginalExtractionAutoDeliveryAndRestart'
managed = successful(logs['real-managed-knowledge-race-20m.log'])
assert re.findall(r'^--- PASS: (\S+) ', managed, re.M) == [managed_root]
assert managed.count('original SHA-256 verified') == 16
launch = json.loads(Path('/tmp/c05-business-query-current-scope-launch.json').read_text())
for path, digest in launch['sources'].items():
    assert sha(path) == digest, path

failed = {
    'diagnostic-not-union-before-fix.log': '/tmp/c05-business-query-scope-before-fix-second.log',
    'diagnostic-native-exclusions-before-fix.log': '/tmp/c05-business-query-native-negation-before-fix.log',
    'diagnostic-rpc-execution-gate-expectation.log': '/tmp/c05-business-query-complex-final-first.log',
    'diagnostic-other-knowledge-default-timeout.log': '/tmp/c05-knowledge-other-sources-final-race.log',
}
for path in failed.values():
    assert re.search(r'^FAIL|^--- FAIL', Path(path).read_text(), re.M), path
assert 'panic: test timed out after 10m0s' in Path(failed['diagnostic-other-knowledge-default-timeout.log']).read_text()
other_path = '/tmp/c05-knowledge-other-sources-race-20m.log'
other_body = Path(other_path).read_text()
other_roots = ['TestCrossUser'+mode+'KnowledgeAgentDispatchSharedOriginalExtractionAutoDeliveryAndRestart' for mode in ['Unmanaged', 'Legacy']]
pending = {}
if re.search(r'^ok\s', other_body, re.M):
    successful(other_path)
    assert re.findall(r'^--- PASS: (\S+) ', other_body, re.M) == other_roots
    assert other_body.count('original SHA-256 verified') == 28
    logs['real-unmanaged-legacy-knowledge-race-20m.log'] = other_path
    other_state = {'status': 'accepted', 'accepted': True, 'roots': other_roots, 'sha_checks': 28}
elif re.search(r'^FAIL\s', other_body, re.M):
    failed['diagnostic-other-knowledge-race-20m.log'] = other_path
    other_state = {'status': 'failed_not_accepted', 'accepted': False}
else:
    process = subprocess.run(['ps', '-p', '83894', '-o', 'pid,etime,pcpu,state,args'], capture_output=True, text=True, check=True).stdout
    assert 'integrationtest.test' in process and '-test.timeout=20m0s' in process and '(Unmanaged|Legacy)KnowledgeAgent' in process
    assert not re.search(r'^--- FAIL|WARNING: DATA RACE', other_body, re.M)
    other_state = {'status': 'verified_running', 'accepted': False, 'pid': 83894, 'session_id': 81013,
                   'observed_at_utc': datetime.now(timezone.utc).isoformat(), 'process': process,
                   'log': other_path, 'completed_roots': re.findall(r'^--- PASS: (\S+) ', other_body, re.M)}
    pending['other-knowledge-race-20m-pending.log'] = other_path

previous_artifacts = json.loads((parent / 'artifact-sha256.json').read_text())
for name, digest in previous_artifacts.items():
    assert sha(parent / name) == digest, name
sources = set(json.loads((parent / 'source-sha256.json').read_text()))
sources.add(str(root / 'docs/testing-2026-09-14-c05-query-exclusions-cursor.md'))
sources = {path: sha(path) for path in sorted(sources)}
todo = (root / 'docs/agent-core-capabilities-todo.md').read_text()
assert len(re.findall(r'^- \[[ x]\] ', todo, re.M)) == 25
assert len(re.findall(r'^- \[x\] ', todo, re.M)) == 4
out.mkdir(parents=True, exist_ok=False)
for name, path in {**logs, **failed, **pending}.items():
    shutil.copyfile(path, out / name)
shutil.copyfile(__file__, out / 'archive.py')
shutil.copyfile('/tmp/c05-business-query-current-scope-launch.json', out / 'query-launch-sources.json')
shutil.copyfile('/tmp/c05-knowledge-final-managed-race-20m-live.json', out / 'historical-managed-race-launch.json')
shutil.copyfile('/tmp/runtime-work/go.work', out / 'runtime-go.work')
for path in launch['sources']:
    shutil.copyfile(path, out / (Path(path).name+'.txt'))
for name in ['source-sha256.json', 'artifact-sha256.json', 'acceptance.json']:
    shutil.copyfile(parent / name, out / ('previous-'+name))
(out / 'source-sha256.json').write_text(json.dumps(sources, ensure_ascii=False, indent=2)+'\n')
(out / 'commands.txt').write_text('''Runtime (GOWORK=/tmp/runtime-work/go.work):
go test ./runtime/application/agenthost ./runtime/bootstrap/transport -count=1
go test -race ./runtime/application/agenthost -run '^TestSharedBusiness' -count=1 -v
go test ./runtime/bootstrap/integrationtest -run '^TestCrossUserBusiness(AgentComplexFiltersOriginalCursorAutomaticDeliveryAndAcceptance|OriginalReceiptsThroughRealOwnerRPCAndRestart)$' -count=1 -v
go test -race ./runtime/bootstrap/integrationtest -run '^TestCrossUserManagedKnowledgeAgentDispatchSharedOriginalExtractionAutoDeliveryAndRestart$' -timeout=20m -count=1 -v
go test -race ./runtime/bootstrap/integrationtest -run '^TestCrossUser(Unmanaged|Legacy)KnowledgeAgentDispatchSharedOriginalExtractionAutoDeliveryAndRestart$' -timeout=20m -count=1 -v

The intermediate three-root business log was compiled before native exclusion coverage was added; the current two-root command validates the final scope source and its launch hashes.
The managed Knowledge race used the earlier phase sources recorded in historical-managed-race-launch.json; it does not establish a race acceptance for the new business scope implementation.
The other Knowledge race was compiled before native business scope changes; its Knowledge fixture and Knowledge production sources remain unchanged. It does not establish a race acceptance for the new business scope implementation.
The final direct AgentHost race covers the new scope production logic and all four ORM exclusion combinations.
Only the Go test observation timeout changed after confirmed terminal default-timeout runs. All actual tasks remain 12 steps / 12 calls / 8192 bytes / 90 seconds.
The published Runtime module-set lock remains unchanged; the full bootstrap package is not accepted.
''')
acceptance = {
    'phase_status': 'accepted_query_exclusion_scope_proofs_and_actual_original_cursor_agent_delivery',
    'c05_complete': False, 'complete_todo_items': 4, 'total_todo_items': 25,
    'accepted_logs': list(logs), 'accepted_exit_codes': {name: 0 for name in logs},
    'diagnostic_failed_logs': list(failed), 'pending_logs': list(pending),
    'real_current_business_roots': [query_root, rpc_root],
    'real_current_root_seconds': {name: float(seconds) for name, seconds in passed},
    'actual_shared_execution_steps': 8, 'actual_original_query_receipts': 5,
    'actual_original_sha_checks': 20, 'actual_original_sha_rounds': 4,
    'actual_owner_rpc_read_without_execution_rights': True,
    'actual_owner_rpc_reader_execution_gate_checked': True,
    'actual_execution_authorized_foreign_cursor_rejected': True,
    'actual_execution_authorized_reader_fresh_query_accepted': True,
    'actual_producer_role_preserved_when_execution_grants_withdrawn': True,
    'actual_task_budget_unchanged': {'steps': 12, 'calls': 12, 'output_bytes': 8192, 'timeout_seconds': 90},
    'source_sdk_translation_and_actual_subject_bindings_preserved': True,
    'negation_neq_notin_and_finite_union_coverage_checked': True,
    'real_orm_exclusion_combinations': 4,
    'orm_custom_predicate_uses_sdk_fixture_not_identity_custom_row_policy_publication': True,
    'source_sdk_evaluator_implication_checks': True,
    'no_json_type_intersection_empty_scope_claim': True,
    'real_managed_knowledge_race': {'status': 'accepted', 'roots': [managed_root], 'sha_checks': 16,
                                    'source_baseline': 'historical-managed-race-launch.json'},
    'real_other_knowledge_race': other_state,
    'default_timeout_other_knowledge_command_failed_not_accepted': True,
    'runtime_published_module_set_lock_modified': False,
    'runtime_bootstrap_complete_package_accepted': False,
    'source_manifest': 'source-sha256.json', 'source_files': len(sources),
    'artifact_manifest': 'artifact-sha256.json', 'previous_artifacts_unchanged': len(previous_artifacts),
    'previous_acceptance_sha256': sha(parent / 'acceptance.json'),
    'remaining': ['broader business query expression scopes', 'other multiple-owner old roles and mixed roots',
                  'complete historical admission and execution-source receipts', 'other dependency management',
                  'broader Identity subject exit linkage', 'published Runtime module-set synchronization',
                  'earlier actual professional write race timeouts', 'all later TODO items retained in original order'],
}
(out / 'acceptance.json').write_text(json.dumps(acceptance, ensure_ascii=False, indent=2)+'\n')
artifacts = {str(path.relative_to(out)): sha(path) for path in sorted(out.rglob('*')) if path.is_file()}
(out / 'artifact-sha256.json').write_text(json.dumps(artifacts, ensure_ascii=False, indent=2)+'\n')
for path, digest in sources.items():
    assert sha(path) == digest, path
for name, digest in artifacts.items():
    assert sha(out / name) == digest, name
for name, digest in previous_artifacts.items():
    assert sha(parent / name) == digest, name
print(json.dumps({'sources': len(sources), 'artifacts': len(artifacts), 'sha_checks': 20,
                  'other_knowledge_race': other_state, 'previous_artifacts_unchanged': len(previous_artifacts)}, ensure_ascii=False))
