"""Verify that Docker deployment smoke guardrails cannot be optimized away."""

import subprocess
import sys
import unittest
from pathlib import Path


class DeploymentSmokeTests(unittest.TestCase):
    def test_optimized_python_is_rejected_before_docker_access(self):
        result = subprocess.run(
            [sys.executable, '-B', '-O', str(Path(__file__).with_name('docker-smoke.py'))],
            capture_output=True, text=True, encoding='utf-8', timeout=10)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('Live smoke requires Python assertions', result.stderr)


if __name__ == '__main__':
    unittest.main()
