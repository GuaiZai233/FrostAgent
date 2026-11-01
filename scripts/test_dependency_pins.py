"""Guard the repository's build dependency declarations against floating refs."""

import re
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
ACTION = re.compile(r"^\s*(?:-\s*)?uses:\s*['\"]?([^\s'\"#]+)", re.MULTILINE)
IMAGE = re.compile(r"^\s*image:\s*['\"]?([^\s'\"#]+)", re.MULTILINE)
FROM = re.compile(r"^[ \t]*FROM[ \t]+(?:--platform=\S+[ \t]+)?(\S+)"
                  r"(?:[ \t]+AS[ \t]+([^\s#]+))?", re.MULTILINE | re.IGNORECASE)
SYNTAX = re.compile(r"^#\s*syntax=(\S+)", re.MULTILINE)
GO_TOOL = re.compile(
    r"(?:\bgo|\$\(GO\))\s+(?:install|run)\s+"
    r"(?:-[^\s=\"';\\]+(?:=(?:\"[^\"]*\"|'[^']*'|[^\s;\\]+))?\s+)*"
    r"([^\s;\\]+)"
)
COMMIT = re.compile(r"[^@\s]+@[0-9a-f]{40}\Z")
DIGEST = re.compile(r"[^@\s]+@sha256:[0-9a-f]{64}\Z")
MODULE_VERSION = re.compile(r"[^@\s]+@v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+incompatible)?\Z")


def workflows():
    return sorted(path for path in (ROOT / '.github' / 'workflows').iterdir()
                  if path.suffix in {'.yml', '.yaml'})


def external_base_images(source):
    stages = {'scratch'}
    images = []
    for reference, alias in FROM.findall(source):
        if reference.lower() not in stages:
            images.append(reference)
        if alias:
            stages.add(alias.lower())
    return images


def unpinned_base_images(source):
    return [reference for reference in external_base_images(source)
            if '$' in reference or not DIGEST.fullmatch(reference)]


class DependencyPinTests(unittest.TestCase):
    def test_go_tool_check_skips_equals_form_build_flags(self):
        for command in ('$(GO) run -ldflags="$(BUILD_LDFLAGS)" ./cmd/app',
                        'go run -trimpath -ldflags="-X example.test.Version=fixture" ./cmd/app'):
            self.assertEqual(GO_TOOL.findall(command), ['./cmd/app'])
        for reference in ('example.invalid/tool@latest', 'example.invalid/tool@v1.2.3'):
            self.assertEqual(GO_TOOL.findall('go run -ldflags="-s -w" ' + reference), [reference])

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

    def test_dockerfile_base_images_use_literal_digests(self):
        source = (ROOT / 'Dockerfile').read_text(encoding='utf-8')
        self.assertTrue(external_base_images(source), 'no external base images found')
        self.assertEqual(unpinned_base_images(source), [])

    def test_removing_any_base_image_digest_is_rejected(self):
        source = (ROOT / 'Dockerfile').read_text(encoding='utf-8')
        images = external_base_images(source)
        self.assertTrue(images, 'no external base images found')
        for reference in images:
            with self.subTest(reference=reference):
                floating = reference.split('@', 1)[0]
                changed = source.replace(reference, floating, 1)
                self.assertIn(floating, unpinned_base_images(changed))

    def test_base_image_check_handles_local_stages_and_platforms(self):
        reference = 'node:22@sha256:' + 'a' * 64
        source = (f'FROM --platform=$BUILDPLATFORM {reference} AS build\n'
                  'from BUILD as reused\n'
                  'FROM scratch AS runtime\n'
                  'FROM runtime AS final\n')
        self.assertEqual(external_base_images(source), [reference])
        self.assertEqual(unpinned_base_images(source), [])

    def test_base_image_check_rejects_variables_and_short_digests(self):
        for reference in ('${BASE_IMAGE}', 'node:${NODE_VERSION}',
                          'node:${NODE_VERSION}@sha256:' + 'a' * 64,
                          'node:22@sha256:' + 'a' * 63):
            with self.subTest(reference=reference):
                self.assertEqual(unpinned_base_images(f'FROM {reference} AS web\n'),
                                 [reference])

    def test_dockerfile_frontend_uses_digest(self):
        source = (ROOT / 'Dockerfile').read_text(encoding='utf-8')
        frontends = SYNTAX.findall(source)
        self.assertTrue(frontends, 'Dockerfile frontend must be explicitly pinned')
        for reference in frontends:
            self.assertRegex(reference, DIGEST)

    def test_dockerfile_installs_pinned_corepack_before_enabling_it(self):
        source = (ROOT / 'Dockerfile').read_text(encoding='utf-8')
        web = re.search(r'^FROM [^\n]+ AS web\n(.*?)(?=^FROM )',
                        source, re.MULTILINE | re.DOTALL)
        self.assertIsNotNone(web, 'web build stage must exist')
        bootstrap = re.search(
            r'RUN npm install --global corepack@\d+\.\d+\.\d+\s*'
            r'(?:\\\n\s*)?&& corepack enable\b', web.group(1))
        self.assertIsNotNone(bootstrap,
                             'install a pinned Corepack before enabling pnpm')
        self.assertLess(bootstrap.end(), web.group(1).index('pnpm install'))

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
