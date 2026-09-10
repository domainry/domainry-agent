import json
import os
from pathlib import Path
import tempfile
import unittest

from agent_private_configuration import load_service_credentials


class CredentialConfigurationTest(unittest.TestCase):
    def test_private_file_and_environment_precedence(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'credentials.json'
            path.write_text(json.dumps({'AGENT_PROVIDER_API_KEY': 'fixture-file-value'}))
            path.chmod(0o600)
            env = {'AGENT_WEB_CREDENTIALS_FILE': str(path)}
            load_service_credentials(env)
            self.assertEqual(env['AGENT_PROVIDER_API_KEY'], 'fixture-file-value')
            env['AGENT_PROVIDER_API_KEY'] = 'fixture-environment-value'
            load_service_credentials(env)
            self.assertEqual(env['AGENT_PROVIDER_API_KEY'], 'fixture-environment-value')
            path.chmod(0o644)
            with self.assertRaises(ValueError):
                load_service_credentials({'AGENT_WEB_CREDENTIALS_FILE': str(path)})

    def test_rejects_unknown_keys_and_invalid_values_without_echo(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'credentials.json'
            for value in [{'PATH': 'fixture-private'}, {'AGENT_PROVIDER_API_KEY': 'fixture-private\n'}, []]:
                path.write_text(json.dumps(value))
                path.chmod(0o600)
                with self.assertRaises(ValueError) as error:
                    load_service_credentials({'AGENT_WEB_CREDENTIALS_FILE': str(path)})
                self.assertNotIn('fixture-private', str(error.exception))


if __name__ == '__main__':
    unittest.main()
