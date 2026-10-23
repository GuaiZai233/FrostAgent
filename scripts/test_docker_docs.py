"""Check the documented bootstrap command without requiring Docker."""

import importlib.util
import os
import subprocess
import unittest
from pathlib import Path
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location('docker_smoke', ROOT / 'scripts/docker-smoke.py')
SMOKE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SMOKE)
TOKEN_KEY = 'FROSTAGENT_BOOTSTRAP_TOKEN'


class DockerGuideTests(unittest.TestCase):
    def invoke(self, document, token=None, returncode=0, stderr='', success=True):
        result = subprocess.CompletedProcess([], returncode, '{"success": true}', stderr)
        with patch.object(SMOKE.subprocess, 'run', return_value=result) as run:
            SMOKE.bootstrap_from_docs(ROOT, document, 'synthetic-container',
                                      'https://bootstrap.example.invalid', token, success)
        run.assert_called_once()
        return run.call_args

    def test_both_guides_supply_the_same_executable_command(self):
        self.assertEqual(SMOKE.BOOTSTRAP_DOCS, ('docs/docker.en.md', 'docs/docker.md'))
        snippets = []
        for document in SMOKE.BOOTSTRAP_DOCS:
            with self.subTest(document=document):
                call = self.invoke(document)
                self.assertEqual(call.args[0], [
                    'docker', 'run', '--rm', '-i', '--env', TOKEN_KEY,
                    '--network', 'container:synthetic-container', 'python:3.13-alpine',
                    'python', '-', 'https://bootstrap.example.invalid',
                ])
                compile(call.kwargs['input'], document, 'exec')
                snippets.append(call.kwargs['input'])
        self.assertEqual(snippets[0], snippets[1])

    def test_token_is_explicit_not_inherited(self):
        with patch.dict(os.environ, {TOKEN_KEY: 'synthetic-inherited-token'}):
            for document in SMOKE.BOOTSTRAP_DOCS:
                for token in (None, 'synthetic-control-token', 'synthetic-incorrect-token'):
                    with self.subTest(document=document, token=token):
                        env = self.invoke(document, token).kwargs['env']
                        if token is None:
                            self.assertNotIn(TOKEN_KEY, env)
                        else:
                            self.assertEqual(env[TOKEN_KEY], token)

    def test_expected_unauthorized_result(self):
        for document in SMOKE.BOOTSTRAP_DOCS:
            with self.subTest(document=document):
                self.invoke(document, returncode=1, stderr='HTTP Error 401', success=False)

    def test_unexpected_failures_are_not_accepted(self):
        for document in SMOKE.BOOTSTRAP_DOCS:
            with self.subTest(document=document):
                with self.assertRaises(AssertionError):
                    self.invoke(document, returncode=1, stderr='connection refused')
                with self.assertRaises(AssertionError):
                    self.invoke(document, returncode=1, stderr='HTTP Error 403', success=False)
                with self.assertRaises(AssertionError):
                    self.invoke(document, success=False)


if __name__ == '__main__':
    unittest.main()
