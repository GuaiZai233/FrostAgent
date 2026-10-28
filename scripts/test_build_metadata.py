"""Regression coverage for version-tag identity and CI/Docker plumbing."""

import os
import subprocess
import sys
import unittest
from pathlib import Path

from build_metadata import resolve_metadata


ROOT = Path(__file__).resolve().parents[1]
COMMIT = 'a1b2c3d' + 'e' * 33


class BuildMetadataTests(unittest.TestCase):
    def test_source_builds_use_commit_even_if_source_has_a_version_tag(self):
        for ref in ('refs/heads/main', 'refs/heads/feature', 'refs/pull/42/merge', ''):
            with self.subTest(ref=ref):
                self.assertEqual(resolve_metadata(ref, COMMIT), {
                    'version': '', 'commit': COMMIT, 'display_version': 'a1b2c3d'})

    def test_version_tag_builds_preserve_supported_semver(self):
        for version in ('0.1.0', '1.2.3', '1.2.3-rc.1', '1.2.3-0',
                        '1.2.3-alpha.0.01abc', '1.2.3-RC.1', '1.2.3-build.1'):
            with self.subTest(version=version):
                self.assertEqual(resolve_metadata('refs/tags/v' + version, COMMIT), {
                    'version': version, 'commit': COMMIT, 'display_version': version})

    def test_invalid_or_shell_like_tags_fail_closed(self):
        for tag in ('release', 'v1', 'v1.2', 'v01.2.3', 'v1.2.03', 'v1.2.3-',
                    'v1.2.3-01', 'v1.2.3-a..b', 'v1.2.3+', 'v1.2.3+build..2',
                    'v1.2.3\ncommit=injected', 'v1.2.3;echo unsafe',
                    'v1.2.3$(echo unsafe)', 'v1.2.3\" -X unrelated.setting=value'):
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                resolve_metadata('refs/tags/' + tag, COMMIT)

    def test_build_metadata_cannot_collide_with_prerelease_image_tags(self):
        # The pinned metadata-action replaces '+' with '-'. Only the original
        # prerelease tag may reach it; the colliding build-metadata ref must fail.
        for metadata_tag, prerelease_tag in (
            ('v1.2.3+build.1', 'v1.2.3-build.1'),
            ('v1.2.3-rc.1+build.2', 'v1.2.3-rc.1-build.2'),
        ):
            with self.subTest(metadata_tag=metadata_tag):
                with self.assertRaisesRegex(ValueError, 'build metadata'):
                    resolve_metadata('refs/tags/' + metadata_tag, COMMIT)
                self.assertEqual(resolve_metadata('refs/tags/' + prerelease_tag, COMMIT), {
                    'version': prerelease_tag[1:], 'commit': COMMIT,
                    'display_version': prerelease_tag[1:]})

    def test_build_metadata_cli_fails_without_emitting_workflow_outputs(self):
        for tag in ('v1.2.3+build.1', 'v1.2.3-rc.1+build.2'):
            with self.subTest(tag=tag):
                result = subprocess.run(
                    [sys.executable, str(ROOT / 'scripts/build_metadata.py')],
                    cwd=ROOT, env={**os.environ, 'GITHUB_REF': 'refs/tags/' + tag},
                    capture_output=True, text=True, encoding='utf-8', check=False)
                self.assertEqual(result.returncode, 1)
                self.assertEqual(result.stdout, '')
                self.assertIn('build metadata', result.stderr)

    def test_release_tags_respect_docker_length_limit(self):
        prefix = 'v1.2.3-'
        tag = prefix + 'a' * (128 - len(prefix))
        self.assertEqual(resolve_metadata('refs/tags/' + tag, COMMIT), {
            'version': tag[1:], 'commit': COMMIT, 'display_version': tag[1:]})
        with self.assertRaisesRegex(ValueError, '128-character limit'):
            resolve_metadata('refs/tags/' + tag + 'a', COMMIT)

    def test_invalid_commits_fail_closed(self):
        for commit in ('', 'a1b2c3d', 'g' * 40, COMMIT + '\nversion=injected'):
            with self.subTest(commit=commit), self.assertRaises(ValueError):
                resolve_metadata('refs/heads/main', commit)
        self.assertEqual(resolve_metadata('refs/heads/main', COMMIT + 'a' * 24)['display_version'], 'a1b2c3d')

    def test_workflow_carries_tested_metadata_into_publish(self):
        workflow = (ROOT / '.github/workflows/docker.yml').read_text(encoding='utf-8')
        self.assertIn('run: python3 scripts/build_metadata.py >> "$GITHUB_OUTPUT"', workflow)
        self.assertLess(workflow.index('name: Resolve build metadata'),
                        workflow.index('name: Build image'))
        self.assertIn('needs: docker', workflow.split('  publish:', 1)[1])
        self.assertNotIn('continue-on-error:', workflow)
        self.assertIn('ref: ${{ needs.docker.outputs.commit }}', workflow)
        self.assertEqual(workflow.count('FROSTAGENT_VERSION=${{ steps.build.outputs.version }}'), 2)
        self.assertEqual(workflow.count('FROSTAGENT_COMMIT=${{ steps.build.outputs.commit }}'), 2)
        self.assertIn('FROSTAGENT_VERSION=${{ needs.docker.outputs.version }}', workflow)
        self.assertIn('FROSTAGENT_COMMIT=${{ needs.docker.outputs.commit }}', workflow)
        self.assertIn('FROSTAGENT_EXPECTED_VERSION: ${{ steps.build.outputs.display_version }}', workflow)
        self.assertIn("type=sha,prefix=sha-,enable=${{ github.ref_type != 'tag' }}", workflow)
        self.assertIn('type=raw,value=latest,enable={{is_default_branch}}', workflow)
        self.assertIn('flavor: latest=false', workflow)

    def test_docker_and_make_pass_the_linker_symbols(self):
        docker = (ROOT / 'Dockerfile').read_text(encoding='utf-8')
        make = (ROOT / 'Makefile').read_text(encoding='utf-8')
        for field in ('VERSION', 'COMMIT'):
            self.assertIn('ARG FROSTAGENT_' + field, docker)
        self.assertIn('internal/buildinfo.Version=${FROSTAGENT_VERSION}', docker)
        self.assertIn('internal/buildinfo.Commit=${FROSTAGENT_COMMIT}', docker)
        self.assertIn('internal/buildinfo.Commit=$(BUILD_COMMIT)', make)
        self.assertNotIn('git describe', make)
        self.assertEqual(make.count('-ldflags="$(BUILD_LDFLAGS)"'), 5)


if __name__ == '__main__':
    unittest.main()
