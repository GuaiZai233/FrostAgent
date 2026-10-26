"""Guard the repository's build dependency declarations against floating refs."""

import re
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
ACTION = re.compile(r"^\s*(?:-\s*)?uses:\s*['\"]?([^\s'\"#]+)", re.MULTILINE)
IMAGE = re.compile(r"^\s*image:\s*['\"]?([^\s'\"#]+)", re.MULTILINE)
SYNTAX = re.compile(r"^#\s*syntax=(\S+)", re.MULTILINE)
GO_TOOL = re.compile(r"(?:\bgo|\$\(GO\))\s+(?:install|run)\s+([^\s;\\]+)")
COMMIT = re.compile(r"[^@\s]+@[0-9a-f]{40}\Z")
DIGEST = re.compile(r"[^@\s]+@sha256:[0-9a-f]{64}\Z")
MODULE_VERSION = re.compile(r"[^@\s]+@v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+incompatible)?\Z")


def workflows():
    return sorted(path for path in (ROOT / '.github' / 'workflows').iterdir()
                  if path.suffix in {'.yml', '.yaml'})


class DependencyPinTests(unittest.TestCase):
    def test_external_actions_use_full_commit_shas(self):
        count = 0
        for path in workflows():
            for reference in ACTION.findall(path.read_text(encoding='utf-8')):
                if reference.startswith('./'):
                    continue
                count += 1
                with self.subTest(file=path.name, reference=reference):
                    pattern = DIGEST if reference.startswith('docker://') else COMMIT
                    self.assertRegex(reference, pattern)
        self.assertGreater(count, 0, 'no external Actions found')

    def test_dockerfile_frontend_uses_digest(self):
        source = (ROOT / 'Dockerfile').read_text(encoding='utf-8')
        frontends = SYNTAX.findall(source)
        self.assertTrue(frontends, 'Dockerfile frontend must be explicitly pinned')
        for reference in frontends:
            self.assertRegex(reference, DIGEST)

    def test_workflow_container_images_use_digests(self):
        count = 0
        for path in workflows():
            for reference in IMAGE.findall(path.read_text(encoding='utf-8')):
                count += 1
                with self.subTest(file=path.name, reference=reference):
                    self.assertRegex(reference, DIGEST)
        self.assertGreater(count, 0, 'no workflow service images found')

    def test_remote_go_tools_use_explicit_versions(self):
        paths = workflows() + [ROOT / name for name in
                               ('Dockerfile', 'Makefile', 'README.md', 'README_zh_CN.md')]
        count = 0
        for path in paths:
            for reference in GO_TOOL.findall(path.read_text(encoding='utf-8')):
                if reference.startswith('./'):
                    continue
                count += 1
                with self.subTest(file=path.name, reference=reference):
                    self.assertRegex(reference, MODULE_VERSION)
        self.assertGreater(count, 0, 'no remote Go tools found')

    def test_reference_patterns_reject_floating_and_short_refs(self):
        for reference in ('actions/checkout@v4', 'actions/checkout@main',
                          'actions/checkout@0123456', 'actions/checkout@' + 'a' * 39):
            with self.subTest(reference=reference):
                self.assertIsNone(COMMIT.fullmatch(reference))
        for reference in ('node:22', 'debian:bookworm-slim',
                          'node@sha256:' + 'a' * 63, 'node@sha256:' + 'g' * 64):
            with self.subTest(reference=reference):
                self.assertIsNone(DIGEST.fullmatch(reference))
        for reference in ('example.invalid/tool@latest', 'example.invalid/tool@main',
                          'example.invalid/tool@v1', 'example.invalid/tool@v1.2'):
            with self.subTest(reference=reference):
                self.assertIsNone(MODULE_VERSION.fullmatch(reference))

    def test_reference_patterns_accept_immutable_refs(self):
        self.assertIsNotNone(COMMIT.fullmatch('actions/checkout@' + 'a' * 40))
        self.assertIsNotNone(DIGEST.fullmatch('node:22@sha256:' + 'a' * 64))
        self.assertIsNotNone(MODULE_VERSION.fullmatch('example.invalid/tool@v1.2.3'))
        self.assertIsNotNone(MODULE_VERSION.fullmatch(
            'example.invalid/tool@v0.0.0-20260101000000-012345abcdef'))


if __name__ == '__main__':
    unittest.main()
