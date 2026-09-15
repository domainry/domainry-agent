import hashlib
import json
import re
import shutil
import subprocess
from datetime import datetime, timezone
from pathlib import Path

root = Path('/Users/tiger/Projects/domainry-agent')
runtime = root.parent / 'domainry-runtime'
sdk = root.parent / 'domainry-agent-sdk'
knowledge = root.parent / 'domainry-knowledge'
parent = root / 'docs/evidence/2026-09-14-c05-business-write-agent'
out = root / 'docs/evidence/2026-09-14-c05-knowledge-shared-readers'

def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()

logs = {
    'core-sdk-knowledge.log': '/tmp/c05-knowledge-shared-all-after-late-fix.log',
    'provider-real-http.log': '/tmp/c05-knowledge-shared-provider-fourth.log',
    'shared-ports-race.log': '/tmp/c05-knowledge-shared-ports-race.log',
    'runtime-source-applications.log': '/tmp/c05-knowledge-shared-runtime-app-owners-final.log',
    'runtime-policy-race.log': '/tmp/c05-knowledge-shared-runtime-policy-race.log',
    'runtime-binding.log': '/tmp/c05-knowledge-shared-runtime-binding.log',
    'real-knowledge-agent.log': '/tmp/c05-knowledge-shared-real-ninth.log',
}
failed = {
    'diagnostic-agent-extraction-fixture.log': '/tmp/c05-knowledge-shared-second.log',
    'diagnostic-library-authorizer-startup.log': '/tmp/c05-knowledge-shared-real-first.log',
    'diagnostic-private-storage-directory.log': '/tmp/c05-knowledge-shared-real-second.log',
    'diagnostic-stale-reader-role-token.log': '/tmp/c05-knowledge-shared-real-third.log',
    'diagnostic-role-resolved-before-http.log': '/tmp/c05-knowledge-shared-real-fourth.log',
    'diagnostic-exact-stale-http-actor.log': '/tmp/c05-knowledge-shared-real-fifth.log',
    'diagnostic-body-change-catalog-still-readable.log': '/tmp/c05-knowledge-shared-real-sixth.log',
    'diagnostic-body-change-result-conflict.log': '/tmp/c05-knowledge-shared-real-seventh.log',
    'diagnostic-body-change-full-run-conflict.log': '/tmp/c05-knowledge-shared-real-eighth.log',
    'diagnostic-standalone-old-sdk-compile.log': '/tmp/c05-knowledge-shared-provider-first.log',
    'diagnostic-extraction-field-body.log': '/tmp/c05-knowledge-shared-provider-second.log',
    'diagnostic-extraction-field-text.log': '/tmp/c05-knowledge-shared-provider-third.log',
    'diagnostic-late-reader-revocation-before-fix.log': '/tmp/c05-knowledge-shared-late-reader-before-fix.log',
    'diagnostic-published-module-set-contract-lock.log': '/tmp/c05-knowledge-shared-runtime-owners.log',
}
race_path = Path('/tmp/c05-knowledge-shared-real-race.log')
race_body = race_path.read_text()
race_status = 'verified_running'
live = None
if re.search(r'^--- PASS: TestCrossUserManagedKnowledgeAgentDispatchSharedOriginalExtractionAutoDeliveryAndRestart ', race_body, re.M) and re.search(r'^ok\s', race_body, re.M):
    logs['real-knowledge-agent-race.log'] = str(race_path)
    race_status = 'accepted'
elif re.search(r'^FAIL|^--- FAIL', race_body, re.M):
    failed['diagnostic-real-knowledge-agent-race.log'] = str(race_path)
    race_status = 'failed_not_accepted'
else:
    process = subprocess.run(['ps', '-p', '72654', '-o', 'pid,etime,command'], capture_output=True, text=True, check=True).stdout
    assert 'integrationtest.test' in process and 'TestCrossUserManagedKnowledgeAgentDispatchSharedOriginalExtractionAutoDeliveryAndRestart' in process
    live = {'session_id': 80410, 'pid': 72654, 'observed_at_utc': datetime.now(timezone.utc).isoformat(), 'process': process, 'log': str(race_path), 'race_accepted': False}
for name, path in logs.items():
    body = Path(path).read_text()
    assert re.search(r'^ok\s', body, re.M), name
    assert not re.search(r'^FAIL|^--- FAIL|WARNING: DATA RACE', body, re.M), name
for name, path in failed.items():
    assert re.search(r'^FAIL|^--- FAIL', Path(path).read_text(), re.M), name
actual_root = 'TestCrossUserManagedKnowledgeAgentDispatchSharedOriginalExtractionAutoDeliveryAndRestart'
pages = {'knowledge-catalog': 3, 'knowledge-search': 4, 'knowledge-read': 4, 'knowledge-extract': 7}
durations = {}
for name in [name for name in logs if name.startswith('real-knowledge-agent')]:
    body = Path(logs[name]).read_text()
    accepted = re.findall(r'^--- PASS: (\S+) \(([0-9.]+)s\)', body, re.M)
    assert len(accepted) == 1 and accepted[0][0] == actual_root, name
    durations[name] = float(accepted[0][1])
    assert body.count('original SHA-256 verified') == 16, name
    for call, count in pages.items():
        assert len(re.findall(r'Exact professional execution result '+call+r': '+str(count)+r' pages, \d+ bytes, original SHA-256 verified', body)) == 4, (name, call)
assert len(re.findall(r'^--- PASS:', Path(logs['shared-ports-race.log']).read_text(), re.M)) == 4
assert len(re.findall(r'^--- PASS:', Path(logs['runtime-policy-race.log']).read_text(), re.M)) == 2
pin_failure = Path(failed['diagnostic-published-module-set-contract-lock.log']).read_text()
assert '127b37c2149ee2425b6b0155acb3096f61772536213b8e1ad3942cc54b683912' in pin_failure
assert '2c887da570ce8809012e3392be039a740e5167d94b6698fa3cb0c5cb92cbbb34' in pin_failure

previous_sources = json.loads((parent / 'source-sha256.json').read_text())
previous_artifacts = json.loads((parent / 'artifact-sha256.json').read_text())
for name, digest in previous_artifacts.items():
    assert sha(parent / name) == digest, name
paths = set(previous_sources)
for base, names in [(root, [
    'docs/agent-core-capabilities-todo.md', 'docs/testing-2026-09-14-c05-knowledge-shared-readers.md',
    'internal/application/conversation_delivery_knowledge_sources.go',
    'internal/application/conversation_delivery_knowledge_sources_test.go',
    'internal/application/conversation_knowledge_tools.go', 'internal/application/conversation_knowledge_extract.go',
    'module/conversation_assembly.go', 'module/factory.go', 'internal/assembly/web/knowledge_libraries.go',
]), (sdk, ['conversation_knowledge_result_read.go', 'conversation_knowledge_result_read_test.go', 'modulehost/conversation.go']),
 (knowledge, [
    'internal/application/knowledge/knowledge_shared_result_read.go',
    'internal/application/knowledge/knowledge_shared_result_read_test.go',
    'internal/infrastructure/provider/knowledge.go',
    'internal/infrastructure/provider/knowledge_shared_result_read.go',
    'internal/infrastructure/provider/knowledge_shared_result_read_test.go',
    'internal/application/knowledge/knowledge_library_source.go',
    'internal/application/knowledge/knowledge_document_source.go',
 ]), (runtime, [
    'runtime/application/agenthost/conversation_business_host.go',
    'runtime/application/agenthost/conversation_knowledge_authorization.go',
    'runtime/application/agenthost/conversation_knowledge_authorization_test.go',
    'runtime/bootstrap/integrationtest/conversation_knowledge_shared_delivery_test.go',
    'runtime/bootstrap/runtime/module_set_composition_test.go', 'config/runtime-module-set.lock.json',
 ])]:
    paths.update(str(base / name) for name in names)
# Do not silently omit expected source paths.
for path in paths:
    assert Path(path).is_file(), path
sources = {path: sha(Path(path)) for path in sorted(paths)}
out.mkdir(parents=True, exist_ok=False)
for name, path in {**logs, **failed}.items():
    shutil.copyfile(path, out / name)
shutil.copyfile(parent / 'source-sha256.json', out / 'previous-source-sha256.json')
shutil.copyfile(parent / 'artifact-sha256.json', out / 'previous-artifact-sha256.json')
shutil.copyfile('/tmp/runtime-work/go.work', out / 'runtime-go.work')
shutil.copyfile(__file__, out / 'archive.py')
if live:
    shutil.copyfile(race_path, out / 'real-knowledge-agent-race-pending.log')
    (out / 'verified-live-race.json').write_text(json.dumps(live, ensure_ascii=False, indent=2)+'\n')
for name in ['c05-knowledge-shared-race-sample.txt', 'c05-knowledge-shared-race-symbolized.txt']:
    shutil.copyfile(Path('/tmp') / name, out / name)
(out / 'source-sha256.json').write_text(json.dumps(sources, ensure_ascii=False, indent=2)+'\n')
(out / 'commands.txt').write_text('''Agent workspace:
go test ./internal/application ./module ../domainry-agent-sdk/... ../domainry-knowledge/... -count=1
go test ../domainry-knowledge/internal/infrastructure/provider -run '^TestSharedKnowledgeProviderUsesActualReaderACLAndPreservesOriginalEvidence$' -count=1 -v
go test -race ./internal/application ../domainry-agent-sdk ../domainry-knowledge/internal/application/knowledge ../domainry-knowledge/internal/infrastructure/provider -run 'Test(SharedKnowledgeReadPreservesAuthorityAndPrivateSourceBoundary|SharedManagedKnowledgeKeepsProducerScopeAndActualReaderFileRights|SharedKnowledgeProviderUsesActualReaderACLAndPreservesOriginalEvidence|ReleasedKnowledgeReceiptKeepsOriginalProducerAndCurrentReader)$' -count=1 -v

Runtime workspace (GOWORK=/tmp/runtime-work/go.work):
go test ./runtime/application/agenthost ./runtime/bootstrap/transport -count=1
go test -race ./runtime/application/agenthost -run '^TestConversationKnowledge' -count=1 -v
go test ./runtime/bootstrap/runtime -run 'Test(OpenAgentBinding|OpenManifestAgentBinding|ReportAnalysis|AgentSDK)' -count=1 -v
go test ./runtime/bootstrap/integrationtest -run '^TestCrossUserManagedKnowledgeAgentDispatchSharedOriginalExtractionAutoDeliveryAndRestart$' -count=1 -v
go test -race ./runtime/bootstrap/integrationtest -run '^TestCrossUserManagedKnowledgeAgentDispatchSharedOriginalExtractionAutoDeliveryAndRestart$' -count=1 -v

Failed diagnostic, not accepted:
go test ./runtime/application/agenthost ./runtime/bootstrap/transport ./runtime/bootstrap/runtime -count=1
Published module set contract lock does not match current local Agent source publication capability. No published version, sum or capability pin was modified.
Failed diagnostic logs retained, including late reader revocation before fix. The live actual race snapshot is not acceptance.
git diff --check: Agent, SDK, Knowledge, Runtime.
''')
artifacts = {p.name: sha(p) for p in sorted(out.iterdir()) if p.is_file() and p.name != 'source-sha256.json'}
(out / 'artifact-sha256.json').write_text(json.dumps(artifacts, ensure_ascii=False, indent=2)+'\n')
acceptance = {
    'phase_status': 'accepted_real_managed_knowledge_agent_shared_original_reader_producer_separation_actual_race_'+race_status,
    'c05_complete': False, 'complete_todo_items': 4, 'total_todo_items': 25,
    'source_manifest': 'source-sha256.json', 'source_files': len(sources),
    'artifact_manifest': 'artifact-sha256.json', 'artifact_files': len(artifacts),
    'accepted_logs': list(logs), 'accepted_exit_codes': {name: 0 for name in logs},
    'diagnostic_failed_logs': list(failed),
    'real_runtime_accepted_roots': [actual_root], 'real_runtime_root_and_race_seconds': durations,
    'real_runtime_race_status': race_status, 'verified_live_race': live,
    'real_identity_users': ['admin', 'professional_source'],
    'real_database_document_storage_indexing_and_upstream_http': True,
    'actual_agent_delegate_user_confirmation': True,
    'actual_agent_tools': ['knowledge_libraries', 'knowledge_search', 'knowledge_read', 'knowledge_extract'],
    'actual_agent_automatic_delivery_and_account_acceptance': True,
    'actual_shared_execution_steps': 7, 'original_result_page_bytes': 256,
    'original_result_pages': pages, 'original_sha_checks_per_actual_run': 16,
    'original_sha_checks_across_accepted_actual_runs': 16 * len(durations),
    'sha_check_phases': ['published', 'same_producer_role_execution_grants_withdrawn', 'restart', 'source_restored'],
    'actual_original_upload_count': 1,
    'original_producer_scope_citations_and_extraction_digest_preserved': True,
    'actual_reader_remote_permission_ids_and_complete_original_json_checked': True,
    'large_json_integer_preserved_in_provider_http_test': '9007199254740993',
    'reader_withdrawal_during_final_producer_io_reproduced_and_fixed': True,
    'managed_local_document_and_membership_checked_after_final_io': True,
    'actual_download_and_producer_membership_withdrawal_http_status': 403,
    'actual_changed_body_and_full_shared_execution_http_status': 409,
    'unchanged_catalog_without_body_stays_readable_on_body_change': True,
    'private_producer_conversation_http_status': 404,
    'fixture_model': True, 'external_model_service_called': False, 'new_frontend_acceptance': False,
    'new_sdk_wire_protocol': False, 'new_persistence_structure': False,
    'existing_execution_or_context_budgets_raised': False,
    'new_task_budget': {'max_steps': 12, 'max_tool_calls': 12, 'max_output_bytes': 8192, 'timeout_seconds': 90},
    'runtime_bootstrap_complete_package_accepted': False,
    'published_module_set_lock_kept_unchanged': True,
    'prior_professional_write_race_accepted': False,
    'previous_phase': parent.name, 'previous_source_manifest_sha256': sha(parent / 'source-sha256.json'),
    'previous_artifact_manifest_sha256': sha(parent / 'artifact-sha256.json'),
    'previous_artifact_files_verified_unchanged': len(previous_artifacts),
    'remaining': ['Other Knowledge source actual Agent chains, including unbound upstream and unmanaged libraries',
                  'more complete query expression scope coverage',
                  'remaining full C05 old roles, history, dependency, source and subject-exit scope',
                  'published Runtime module-set lock versus current local Agent capability release synchronization',
                  'prior professional write race execution timeout remains unaccepted',
                  'all later TODO items retained in order'],
}
(out / 'acceptance.json').write_text(json.dumps(acceptance, ensure_ascii=False, indent=2)+'\n')
for path, digest in sources.items(): assert sha(Path(path)) == digest
for name, digest in artifacts.items(): assert sha(out / name) == digest
for name, digest in previous_artifacts.items(): assert sha(parent / name) == digest
print(json.dumps({'source_files': len(sources), 'artifact_files': len(artifacts), 'accepted_logs': len(logs), 'failed_logs': len(failed), 'previous_artifacts_unchanged': len(previous_artifacts), 'sha_checks': 16*len(durations), 'actual_race_status': race_status}, ensure_ascii=False))
