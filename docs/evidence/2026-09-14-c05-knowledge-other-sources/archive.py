import hashlib
import json
import re
import shutil
import subprocess
from datetime import datetime, timezone
from pathlib import Path

root = Path('/Users/tiger/Projects/domainry-agent')
runtime = root.parent / 'domainry-runtime'
identity = root.parent / 'domainry-identity'
parent = root / 'docs/evidence/2026-09-14-c05-knowledge-shared-readers'
out = root / 'docs/evidence/2026-09-14-c05-knowledge-other-sources'

def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()

def successful(path):
    body = Path(path).read_text()
    assert re.search(r'^ok\s', body, re.M), path
    assert not re.search(r'^FAIL|^--- FAIL|WARNING: DATA RACE', body, re.M), path
    return body

roots = ['TestCrossUser'+mode+'KnowledgeAgentDispatchSharedOriginalExtractionAutoDeliveryAndRestart'
         for mode in ['Managed', 'Unmanaged', 'Legacy']]
logs = {
    'identity-all.log': '/tmp/c05-knowledge-identity-role-all.log',
    'identity-application-adapter-race.log': '/tmp/c05-knowledge-identity-role-race.log',
    'runtime-source-applications.log': '/tmp/c05-knowledge-other-runtime-owners.log',
    'real-three-knowledge-agent-sources.log': '/tmp/c05-knowledge-other-sources-final-real.log',
}
for path in logs.values():
    successful(path)
normal = successful(logs['real-three-knowledge-agent-sources.log'])
passed = re.findall(r'^--- PASS: (\S+) \(([0-9.]+)s\)', normal, re.M)
assert [name for name, seconds in passed] == roots, passed
assert normal.count('original SHA-256 verified') == 44
assert len(re.findall(r'^--- PASS:', normal, re.M)) == 3
assert len(re.findall(r'knowledge-catalog: 3 pages, \d+ bytes, original SHA-256 verified', normal)) == 8
assert len(re.findall(r'knowledge-extract: 7 pages, \d+ bytes, original SHA-256 verified', normal)) == 8
assert len(re.findall(r'knowledge-extract: 6 pages, \d+ bytes, original SHA-256 verified', normal)) == 4

failed = {
    'diagnostic-original-managed-race-default-timeout.log': '/tmp/c05-knowledge-shared-real-race.log',
    'diagnostic-identity-test-field-before-correction.log': '/tmp/c05-knowledge-identity-role-before-fix.log',
    'diagnostic-identity-overwritten-role-query-error.log': '/tmp/c05-knowledge-identity-role-before-fix-second.log',
    'diagnostic-unmanaged-directory-acl-expectation.log': '/tmp/c05-knowledge-unmanaged-real-first.log',
}
assert 'panic: test timed out after 10m0s' in Path(failed['diagnostic-original-managed-race-default-timeout.log']).read_text()
assert 'current role resolution failure was overwritten' in Path(failed['diagnostic-identity-overwritten-role-query-error.log']).read_text()
for path in failed.values():
    assert re.search(r'^FAIL|^--- FAIL', Path(path).read_text(), re.M), path

race_specs = [
    ('managed-agent-race', '/tmp/c05-knowledge-identity-role-real-race.log', 77736, 1178, roots[:1], 16),
    ('unmanaged-legacy-agent-race', '/tmp/c05-knowledge-other-sources-final-race.log', 79696, 72312, roots[1:], 28),
]
race_states, pending = {}, {}
for name, path, pid, session, expected_roots, expected_sha in race_specs:
    body = Path(path).read_text()
    if re.search(r'^ok\s', body, re.M):
        successful(path)
        assert re.findall(r'^--- PASS: (\S+) ', body, re.M) == expected_roots
        assert body.count('original SHA-256 verified') == expected_sha
        logs[name+'.log'] = path
        race_states[name] = {'status': 'accepted', 'roots': expected_roots, 'sha_checks': expected_sha}
    elif re.search(r'^FAIL\s', body, re.M):
        failed['diagnostic-'+name+'.log'] = path
        race_states[name] = {'status': 'failed_not_accepted', 'accepted': False}
    else:
        process = subprocess.run(['ps', '-p', str(pid), '-o', 'pid,etime,pcpu,state,command'],
                                 capture_output=True, text=True, check=True).stdout
        assert 'integrationtest.test' in process and 'KnowledgeAgentDispatchSharedOriginalExtractionAutoDeliveryAndRestart' in process
        state = {'status': 'verified_running', 'accepted': False, 'pid': pid, 'session_id': session,
                 'observed_at_utc': datetime.now(timezone.utc).isoformat(), 'process': process, 'log': path,
                 'observed_failed_root_or_race_warning': bool(re.search(r'^--- FAIL|WARNING: DATA RACE', body, re.M))}
        race_states[name] = state
        pending[name+'-pending.log'] = path

previous_artifacts = json.loads((parent / 'artifact-sha256.json').read_text())
for name, digest in previous_artifacts.items():
    assert sha(parent / name) == digest, name
previous_acceptance_sha = sha(parent / 'acceptance.json')
paths = set(json.loads((parent / 'source-sha256.json').read_text()))
paths.update(str(root / name) for name in [
    'docs/agent-core-capabilities-todo.md',
    'docs/testing-2026-09-14-c05-knowledge-shared-readers.md',
    'docs/testing-2026-09-14-c05-knowledge-other-sources.md',
])
paths.add(str(runtime / 'runtime/bootstrap/integrationtest/conversation_knowledge_shared_delivery_test.go'))
for folder in ['internal', 'module', 'cmd', 'capability']:
    paths.update(str(path) for path in (identity / folder).rglob('*.go'))
paths.update(str(identity / name) for name in ['AGENTS.md', 'go.mod', 'go.sum'])
for path in paths:
    assert Path(path).is_file(), path
sources = {path: sha(path) for path in sorted(paths)}

out.mkdir(parents=True, exist_ok=False)
for name, path in {**logs, **failed, **pending}.items():
    shutil.copyfile(path, out / name)
shutil.copyfile(parent / 'source-sha256.json', out / 'previous-source-sha256.json')
shutil.copyfile(parent / 'artifact-sha256.json', out / 'previous-artifact-sha256.json')
shutil.copyfile('/tmp/runtime-work/go.work', out / 'runtime-go.work')
shutil.copyfile('/Users/tiger/Projects/domainry-agent/go.work', out / 'agent-go.work')
shutil.copyfile('/tmp/c05-knowledge-identity-role.diff', out / 'identity-role.diff')
shutil.copyfile(runtime / 'runtime/bootstrap/integrationtest/conversation_knowledge_shared_delivery_test.go', out / 'runtime-knowledge-agent-test.go.txt')
shutil.copyfile(__file__, out / 'archive.py')
(out / 'source-sha256.json').write_text(json.dumps(sources, ensure_ascii=False, indent=2)+'\n')
(out / 'race-states.json').write_text(json.dumps(race_states, ensure_ascii=False, indent=2)+'\n')
(out / 'commands.txt').write_text('''Identity (GOWORK=/Users/tiger/Projects/domainry-agent/go.work):
go test ./internal/application/identity -run '^TestIdentityRoleSnapshotPropagatesCurrentResolutionFailureAndKeepsRoleIsolation$' -count=1 -v
go test ./... -count=1
go test -race ./internal/application/identity ./internal/adapter/identitysdk -count=1

Runtime (GOWORK=/tmp/runtime-work/go.work):
go test ./runtime/application/agenthost ./runtime/bootstrap/transport -count=1
go test ./runtime/bootstrap/integrationtest -run '^TestCrossUser(Managed|Unmanaged|Legacy)KnowledgeAgentDispatchSharedOriginalExtractionAutoDeliveryAndRestart$' -count=1 -v
go test -race ./runtime/bootstrap/integrationtest -run '^TestCrossUserManagedKnowledgeAgentDispatchSharedOriginalExtractionAutoDeliveryAndRestart$' -count=1 -v
go test -race ./runtime/bootstrap/integrationtest -run '^TestCrossUser(Unmanaged|Legacy)KnowledgeAgentDispatchSharedOriginalExtractionAutoDeliveryAndRestart$' -count=1 -v

All actual task budgets remain 12 steps / 12 calls / 8192 bytes / 90 seconds.
All actual race commands use the unchanged default Go test timeout of 10 minutes.
Pending race logs and specific live process observations are not successful test acceptance.
The original managed actual race default timeout is retained as failure.
The published Runtime module-set capability lock remains unchanged; full bootstrap package is not accepted.
git diff --check: Agent, SDK, Knowledge, Identity, Runtime.
''')
todo = (root / 'docs/agent-core-capabilities-todo.md').read_text()
assert len(re.findall(r'^- \[[ x]\] ', todo, re.M)) == 25
assert len(re.findall(r'^- \[x\] ', todo, re.M)) == 4
acceptance = {
    'phase_status': 'accepted_identity_exact_role_resolution_and_real_unmanaged_legacy_knowledge_agent_sources',
    'c05_complete': False, 'complete_todo_items': 4, 'total_todo_items': 25,
    'accepted_logs': list(logs), 'accepted_exit_codes': {name: 0 for name in logs},
    'diagnostic_failed_logs': list(failed), 'pending_race_logs': list(pending),
    'real_runtime_accepted_roots': roots,
    'real_runtime_root_seconds': {name: float(seconds) for name, seconds in passed},
    'real_runtime_race_states': race_states,
    'source_manifest': 'source-sha256.json', 'source_files': len(sources),
    'artifact_manifest': 'artifact-sha256.json',
    'identity_full_repository_accepted': True, 'identity_application_sdk_adapter_race_accepted': True,
    'identity_current_resolution_failure_propagated': True,
    'identity_selected_role_isolation_and_withdrawal_checked': True,
    'no_authorization_cache_added': True, 'sdk_authorization_revision_fence_preserved': True,
    'normal_actual_original_sha_checks': 44,
    'normal_actual_shared_execution_steps': {'managed': 7, 'unmanaged': 7, 'legacy': 6},
    'normal_original_sha_check_rounds_per_source': 4,
    'normal_actual_original_upload_count': {'managed': 1, 'unmanaged': 0, 'legacy': 0},
    'actual_upstream_distinct_reader_and_producer_acls_checked': True,
    'actual_extraction_amount_original_document_and_citation_checked_before_delivery': True,
    'actual_task_budget_unchanged': {'steps': 12, 'calls': 12, 'output_bytes': 8192, 'timeout_seconds': 90},
    'actual_result_page_bytes': 256,
    'original_managed_race_default_timeout_failed_not_accepted': True,
    'runtime_published_module_set_lock_modified': False,
    'runtime_bootstrap_complete_package_accepted': False,
    'previous_artifacts_unchanged': len(previous_artifacts),
    'previous_acceptance_sha256': previous_acceptance_sha,
    'remaining': ['broader business query expression scopes', 'other multiple-owner old roles and mixed roots',
                  'complete historical admission and execution-source receipts', 'other dependency management',
                  'broader Identity subject exit linkage', 'published Runtime module-set synchronization',
                  'all later TODO items retained in original order'],
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
assert sha(parent / 'acceptance.json') == previous_acceptance_sha
print(json.dumps({'sources': len(sources), 'artifacts': len(artifacts), 'accepted_logs': len(logs),
                  'failed_logs': len(failed), 'normal_sha_checks': 44, 'race_states': race_states,
                  'previous_artifacts_unchanged': len(previous_artifacts)}, ensure_ascii=False))
