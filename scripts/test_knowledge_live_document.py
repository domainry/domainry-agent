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
                            state.update(document=request.data.decode(), pending=2)
                            state['uploads'] += 1
                        elif request.method == 'DELETE':
                            state['document'] = None
                            state['deletes'] += 1
                        else:
                            raise AssertionError(request.method)
                        return Response({'err_code': 0, 'data': {'ok': True}})
                    if path.endswith('/fetch'):
                        state['fetches'] += 1
                        if state['document'] is None:
                            return Response({'err_code': 1004})
                        if state['pending']:
                            status = 'PENDING' if state['pending'] == 2 else 'CHUNKED'
                            state['pending'] -= 1
                            return Response({'err_code': 0, 'data': {'status': status, 'chunks': []}})
                        return Response({'err_code': 0, 'data': {'status': 'INDEXED', 'chunks': [{'content': state['document']}]}})
                    if path.endswith('/search'):
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

            run('--create')
            manifest = evidence / 'manifest.json'
            self.assertTrue(json.loads(manifest.read_text())['content_readable'])
            self.assertGreaterEqual(state['fetches'], 3)  # preflight, pending, indexed
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
