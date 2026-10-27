"""Regression coverage for version-tag identity and CI/Docker plumbing."""

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

    def test_version_tag_builds_preserve_semver(self):
        for version in ('0.1.0', '1.2.3', '1.2.3-rc.1', '1.2.3-0',
                        '1.2.3-alpha.0.01abc', '1.2.3+build.01', '1.2.3-rc.1+build.2'):
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

    def test_invalid_commits_fail_closed(self):
        for commit in ('', 'a1b2c3d', 'g' * 40, COMMIT + '\nversion=injected'):
            with self.subTest(commit=commit), self.assertRaises(ValueError):
                resolve_metadata('refs/heads/main', commit)
        self.assertEqual(resolve_metadata('refs/heads/main', COMMIT + 'a' * 24)['display_version'], 'a1b2c3d')

    def test_workflow_carries_tested_metadata_into_publish(self):
        workflow = (ROOT / '.github/workflows/docker.yml').read_text(encoding='utf-8')
        self.assertIn('run: python3 scripts/build_metadata.py >> "$GITHUB_OUTPUT"', workflow)
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
