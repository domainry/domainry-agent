"""Lifecycle helper regressions; no network or external credentials required."""
import contextlib
import io
import json
import os
from pathlib import Path
import runpy
import sys
import tempfile
import unittest
from unittest.mock import patch
import urllib.parse


class SyntheticDocumentLifecycleTest(unittest.TestCase):
    def test_pending_index_and_idempotent_cleanup(self):
        self.check_lifecycle()

    def test_private_scope_and_cleanup(self):
        self.check_lifecycle(private=True)

    def test_private_not_found_does_not_skip_delete(self):
        self.check_lifecycle(private=True, hide_before_delete=True)

    def test_failed_private_delete_is_not_reported_as_cleaned(self):
        self.check_lifecycle(private=True, delete_failed=True)

    def check_lifecycle(self, private=False, hide_before_delete=False, delete_failed=False):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            evidence = root / 'evidence'
            evidence.mkdir()
            config = root / 'services.json'
            config.write_text(json.dumps({
                'AGENT_KNOWLEDGE_BASE_URL': 'https://api.verdent.ai',
                'AGENT_KNOWLEDGE_TEAM_ID': 'synthetic-team',
                'AGENT_KNOWLEDGE_KB_ID': 'synthetic-kb',
            }))
            state = {'document': None, 'pending': False, 'uploads': 0, 'deletes': 0, 'fetches': 0}
            test = self

            class Response:
                status = 200

                def __init__(self, value):
                    self.value = value

                def __enter__(self):
                    return self

                def __exit__(self, *args):
                    pass

                def read(self, limit):
                    return json.dumps(self.value).encode()

            class Transport:
                def open(self, request, timeout):
                    path = urllib.parse.urlsplit(request.full_url).path
                    if path.endswith('/documents'):
                        if request.method == 'POST':
                            saved = json.loads((evidence / 'manifest.json').read_text())
                            test.assertTrue(saved['upload_attempted'])
                            headers = {name.lower(): value for name, value in request.header_items()}
                            if private:
                                test.assertEqual(headers['x-kb-permission-ids'], json.dumps(saved['permission_ids'], ensure_ascii=True, separators=(',', ':')))
                                test.assertEqual(headers['x-kb-request-id'], saved['upload_request_id'])
                                state['permission_ids'] = saved['permission_ids']
                            else:
                                test.assertNotIn('x-kb-permission-ids', headers)
                                test.assertNotIn('x-kb-request-id', headers)
                            state.update(document=request.data.decode(), pending=4)
                            state['uploads'] += 1
                        elif request.method == 'DELETE':
                            if state.get('delete_failed'):
                                return Response({'err_code': 1008})
                            state['document'] = None
                            state['deletes'] += 1
                        else:
                            raise AssertionError(request.method)
                        return Response({'err_code': 0, 'data': {'ok': True}})
                    if path.endswith('/fetch'):
                        payload = json.loads(request.data)
                        if private:
                            saved = json.loads((evidence / 'manifest.json').read_text())
                            test.assertEqual(payload['permission_ids'], saved['permission_ids'])
                        else:
                            test.assertNotIn('permission_ids', payload)
                        state['fetches'] += 1
                        if state['document'] is None or state.get('hidden'):
                            return Response({'err_code': 1004})
                        if state['pending']:
                            status = {4: 'PENDING', 3: 'PARSING', 2: 'PARSED', 1: 'CHUNKED'}[state['pending']]
                            state['pending'] -= 1
                            return Response({'err_code': 0, 'data': {'status': status, 'chunks': []}})
                        return Response({'err_code': 0, 'data': {'status': 'INDEXED', 'chunks': [{'content': state['document']}]}})
                    if path.endswith('/search'):
                        payload = json.loads(request.data)
                        if private:
                            test.assertEqual(payload['permission_ids'], state['permission_ids'])
                        else:
                            test.assertNotIn('permission_ids', payload)
                        return Response({'err_code': 0, 'data': {'hits': [{'snippet': state['document']}]}})
                    raise AssertionError('Unexpected outbound request')

            def run(*arguments):
                with patch.dict(os.environ, {'AGENT_WEB_SERVICES_CONFIG': str(config), 'AGENT_KNOWLEDGE_API_KEY': 'synthetic-only'}, clear=True), \
                        patch.object(sys, 'argv', ['knowledge-live-document.py', *arguments]), \
                        patch('urllib.request.build_opener', return_value=Transport()), \
                        patch('tempfile.mkdtemp', return_value=str(evidence)), \
                        patch('time.sleep'), contextlib.redirect_stdout(io.StringIO()):
                    try:
                        runpy.run_path(str(Path(__file__).with_name('knowledge-live-document.py')), run_name='__main__')
                    except SystemExit as result:
                        if result.code != 0:
                            raise

            run('--create', *(['--private'] if private else []))
            manifest = evidence / 'manifest.json'
            self.assertTrue(json.loads(manifest.read_text())['content_readable'])
            self.assertGreaterEqual(state['fetches'], 3)  # preflight, pending, indexed
            if private:
                original = json.loads(manifest.read_text())
                for name, value in (('permission_ids', []), ('permission_id', 'business-permission'), ('upload_request_id', ''), ('visibility', 'public')):
                    manifest.write_text(json.dumps({**original, name: value}))
                    with self.assertRaises(RuntimeError):
                        run('--cleanup', str(manifest))
                    self.assertEqual(state['deletes'], 0)
                manifest.write_text(json.dumps(original))
            state['hidden'] = hide_before_delete
            if delete_failed:
                state['delete_failed'] = True
                with self.assertRaisesRegex(RuntimeError, 'Deletion did not report success'):
                    run('--cleanup', str(manifest))
                saved = json.loads(manifest.read_text())
                self.assertFalse(saved['cleanup_verified'])
                self.assertNotIn('delete_acknowledged', saved)
                self.assertIsNotNone(state['document'])
                state['delete_failed'] = False
            run('--cleanup', str(manifest))
            self.assertTrue(json.loads(manifest.read_text())['cleanup_verified'])
            run('--cleanup', str(manifest))
            self.assertEqual((state['uploads'], state['deletes']), (1, 1))

            saved = json.loads(manifest.read_text())
            saved['doc_id'] = 'existing-business-document'
            manifest.write_text(json.dumps(saved))
            with self.assertRaisesRegex(RuntimeError, 'synthetic document'):
                run('--cleanup', str(manifest))
            self.assertEqual(state['deletes'], 1)


if __name__ == '__main__':
    unittest.main()
