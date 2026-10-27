"""Synthetic regression tests; no registry calls or credentials required."""

import copy
import io
import json
import unittest
from datetime import datetime, timedelta, timezone
from email.message import Message
from pathlib import Path
from unittest.mock import patch
from urllib.error import HTTPError, URLError

import ghcr_cleanup as gc


ROOT = Path(__file__).resolve().parents[1]


def digest(number):
    return f'sha256:{number:064x}'


def version(number, *tags, created_at=None):
    if created_at is None:
        created_at = (
            datetime(2026, 1, 1, tzinfo=timezone.utc) + timedelta(seconds=number)
        ).isoformat().replace('+00:00', 'Z')
    return {
        'id': number,
        'name': digest(number),
        'created_at': created_at,
        'metadata': {'container': {'tags': list(tags)}},
    }


def leaf():
    return {'schemaVersion': 2, 'mediaType': 'application/vnd.oci.image.manifest.v1+json',
            'config': {'digest': digest(999)}, 'layers': []}


def index(*children):
    return {'schemaVersion': 2, 'mediaType': 'application/vnd.oci.image.index.v1+json',
            'manifests': [{'digest': digest(child)} for child in children]}


class Registry:
    def __init__(self):
        self.items = [
            version(1, 'main', 'latest', 'sha-aaaaaaa', created_at='2026-01-07T00:00:00Z'),
            version(2, 'v1.0.1', 'sha-bbbbbbb', created_at='2026-01-05T00:00:00Z'),
            version(3, 'v1.1.0-rc.1', created_at='2026-01-06T00:00:00Z'),
            version(4, created_at='2026-01-01T00:00:00Z'),
            version(5, created_at='2026-01-02T00:00:00Z'),
            version(6, 'sha-ccccccc', created_at='2026-01-03T00:00:00Z'),
            version(7, created_at='2026-01-04T00:00:00Z'),
        ]
        self.manifests = {digest(1): index(4, 5), digest(2): index(4), digest(3): leaf(),
                          digest(4): leaf(), digest(5): leaf(), digest(6): leaf()}
        self.deleted = []
        self.reads = []
        self.list_calls = 0

    def versions(self):
        self.list_calls += 1
        return copy.deepcopy(self.items)

    def manifest(self, reference):
        self.reads.append(reference)
        resolved = reference
        if not gc.DIGEST.fullmatch(reference):
            resolved = next(item['name'] for item in self.items
                            if reference in item['metadata']['container']['tags'])
        return resolved, copy.deepcopy(self.manifests[resolved])

    def version(self, version_id):
        return copy.deepcopy(next(item for item in self.items if item['id'] == version_id))

    def delete(self, version_id):
        self.deleted.append(version_id)


class RetentionTests(unittest.TestCase):
    def setUp(self):
        self.client = Registry()

    def run_cleanup(self, **kwargs):
        return gc.cleanup(self.client, log=lambda _: None, **kwargs)

    def test_release_tag_policy(self):
        for tag in ('main', 'latest', 'v1.0.1', '1.2.3', 'v2.0.0-rc.1', 'v1.2.3-build.42'):
            with self.subTest(tag=tag):
                self.assertTrue(gc.protected_tag(tag))
        for tag in ('sha-abcdef1', 'main-old', 'latest-old', 'vnext', 'v1', 'v1.0', 'feature', 'v1.2.3\n'):
            with self.subTest(tag=tag):
                self.assertFalse(gc.protected_tag(tag))

    def test_dry_run_is_default_and_retains_all_releases_and_shared_children(self):
        planned = self.run_cleanup()
        self.assertEqual([item['id'] for item in planned], [7])
        self.assertEqual(self.client.deleted, [])
        self.assertEqual(self.client.list_calls, 1)
        self.assertIn('sha-ccccccc', self.client.reads)

    def test_apply_deletes_only_expired_commits_and_orphans(self):
        self.run_cleanup(apply=True)
        self.assertEqual(self.client.deleted, [7])
        self.assertEqual(self.client.list_calls, 2)

    def test_retains_latest_ten_intermediate_sha_images(self):
        for number in range(100, 112):
            created_at = (
                datetime(2026, 1, 8, tzinfo=timezone.utc) + timedelta(seconds=number)
            ).isoformat().replace('+00:00', 'Z')
            self.client.items.append(version(number, f'sha-{number:07x}', created_at=created_at))
            self.client.manifests[digest(number)] = leaf()
        self.client.items.append(version(999, 'sha-9999999', created_at='2026-01-02T00:00:00Z'))
        self.client.manifests[digest(999)] = leaf()

        self.run_cleanup(apply=True)

        self.assertEqual(set(self.client.deleted), {6, 7, 100, 101, 999})
        self.assertTrue(set(range(102, 112)).isdisjoint(self.client.deleted))

    def test_main_update_keeps_previous_main_within_intermediate_retention(self):
        self.client.items[0]['metadata']['container']['tags'] = ['sha-eeeeeee']
        self.client.items.append(version(8, 'main', 'latest', 'sha-ffffffff', created_at='2026-01-08T00:00:00Z'))
        self.client.manifests[digest(8)] = leaf()
        self.run_cleanup(apply=True)
        self.assertEqual(self.client.deleted, [7])
        self.assertNotIn(1, self.client.deleted, 'previous main remains within the ten-image window')

    def test_release_remains_after_main_moves_even_when_it_has_sha_tag(self):
        self.client.items[0]['metadata']['container']['tags'] = ['v1.2.0', 'sha-eeeeeee']
        self.client.items.append(version(8, 'main', 'latest', 'sha-ffffffff', created_at='2026-01-08T00:00:00Z'))
        self.client.manifests[digest(8)] = leaf()
        self.run_cleanup(apply=True)
        self.assertEqual(self.client.deleted, [7])

    def test_nested_indexes_and_attestation_subjects_remain(self):
        self.client.manifests[digest(5)] = index(8)
        self.client.manifests[digest(8)] = leaf()
        self.client.manifests[digest(8)]['subject'] = {'digest': digest(9)}
        self.client.manifests[digest(9)] = leaf()
        self.client.items.extend([version(8), version(9, 'sha-9999999')])
        self.run_cleanup(apply=True)
        self.assertEqual(self.client.deleted, [7])

    def test_untagged_old_index_is_deleted_not_kept_by_age(self):
        self.client.items.append(version(10))
        self.run_cleanup(apply=True)
        self.assertEqual(self.client.deleted, [7, 10])

    def test_missing_or_divergent_main_aliases_stop_all_deletion(self):
        for names in ([], ['main'], ['latest']):
            with self.subTest(names=names):
                self.client = Registry()
                self.client.items[0]['metadata']['container']['tags'] = names
                with self.assertRaisesRegex(gc.CleanupError, 'main and latest'):
                    self.run_cleanup(apply=True)
                self.assertEqual(self.client.deleted, [])
        self.client = Registry()
        self.client.items[0]['metadata']['container']['tags'] = ['main']
        self.client.items[-1]['metadata']['container']['tags'] = ['latest']
        with self.assertRaisesRegex(gc.CleanupError, 'main and latest'):
            self.run_cleanup(apply=True)
        self.assertEqual(self.client.deleted, [])

    def test_registry_tag_mismatch_stops_all_deletion(self):
        original = self.client.manifest
        self.client.manifest = lambda ref: (digest(90), leaf()) if ref == 'main' else original(ref)
        with self.assertRaisesRegex(gc.CleanupError, 'not yet consistent'):
            self.run_cleanup(apply=True)
        self.assertEqual(self.client.deleted, [])

    def test_missing_child_or_registry_error_stops_all_deletion(self):
        original = self.client.manifest

        def missing(ref):
            if ref == digest(5):
                raise gc.CleanupError('registry unavailable')
            return original(ref)

        self.client.manifest = missing
        with self.assertRaisesRegex(gc.CleanupError, 'registry unavailable'):
            self.run_cleanup(apply=True)
        self.assertEqual(self.client.deleted, [])

    def test_invalid_manifest_stops_all_deletion(self):
        bad_manifests = [None, {}, {**leaf(), 'mediaType': 'unknown'},
                         {**leaf(), 'schemaVersion': 1}, index(),
                         {**index(4), 'manifests': [{'digest': 'invalid'}]},
                         {**index(4), 'manifests': None},
                         {**leaf(), 'config': None}, {**leaf(), 'subject': {}}]
        for manifest in bad_manifests:
            with self.subTest(manifest=manifest):
                self.client = Registry()
                self.client.manifests[digest(1)] = manifest
                with self.assertRaises(gc.CleanupError):
                    self.run_cleanup(apply=True)
                self.assertEqual(self.client.deleted, [])

    def test_docker_manifest_formats_are_supported(self):
        self.client.manifests[digest(1)]['mediaType'] = 'application/vnd.docker.distribution.manifest.list.v2+json'
        self.client.manifests[digest(4)]['mediaType'] = 'application/vnd.docker.distribution.manifest.v2+json'
        self.run_cleanup(apply=True)
        self.assertEqual(self.client.deleted, [7])

    def test_changed_snapshot_stops_all_deletion(self):
        original = self.client.versions

        def changing():
            items = original()
            if self.client.list_calls > 1:
                items[-1]['metadata']['container']['tags'] = ['v2.0.0']
            return items

        self.client.versions = changing
        with self.assertRaisesRegex(gc.CleanupError, 'changed during planning'):
            self.run_cleanup(apply=True)
        self.assertEqual(self.client.deleted, [])

    def test_candidate_retagged_before_delete_stops_cleanup(self):
        original = self.client.version

        def changed(version_id):
            item = original(version_id)
            item['metadata']['container']['tags'] = ['v2.0.0']
            return item

        self.client.version = changed
        with self.assertRaisesRegex(gc.CleanupError, 'changed before deletion'):
            self.run_cleanup(apply=True)
        self.assertEqual(self.client.deleted, [])

    def test_delete_failure_is_not_silently_ignored(self):
        def forbidden(_):
            raise gc.CleanupError('HTTP 403')

        self.client.delete = forbidden
        with self.assertRaisesRegex(gc.CleanupError, '403'):
            self.run_cleanup(apply=True)

    def test_bad_metadata_or_duplicates_stop_all_deletion(self):
        for item in ({}, version(1), version(10, 'main'),
                     {**version(10), 'name': 'invalid'},
                     {**version(10), 'created_at': None},
                     {**version(10), 'metadata': {'container': {'tags': None}}}):
            with self.subTest(item=item):
                self.client = Registry()
                self.client.items.append(item)
                with self.assertRaises(gc.CleanupError):
                    self.run_cleanup(apply=True)
                self.assertEqual(self.client.deleted, [])

    def test_empty_package_does_not_delete(self):
        self.client.items = []
        with self.assertRaises(gc.CleanupError):
            self.run_cleanup(apply=True)
        self.assertEqual(self.client.deleted, [])


class ClientTests(unittest.TestCase):
    def client(self):
        client = gc.GHCR.__new__(gc.GHCR)
        client.path = '/users/example/packages/container/example/versions'
        client.api_headers = {'Authorization': 'Bearer synthetic-token'}
        client.registry_headers = {'Authorization': 'Bearer synthetic-pull-token'}
        return client

    def test_user_and_org_api_paths_and_registry_credentials(self):
        for owner_type, scope in [('User', 'users'), ('Organization', 'orgs')]:
            with self.subTest(owner_type=owner_type), patch.object(gc, 'request_json') as request:
                request.side_effect = [({'owner': {'type': owner_type}}, {}), ({'token': 'synthetic-pull'}, {})]
                client = gc.GHCR('synthetic-github-token', 'synthetic-actor')
                self.assertEqual(client.path, f'/{scope}/GuaiZai233/packages/container/frostagent/versions')
                self.assertEqual(client.registry_headers['Authorization'], 'Bearer synthetic-pull')
                self.assertEqual(client.api_headers['Authorization'], 'Bearer synthetic-github-token')
                token_call = request.call_args_list[1]
                self.assertIn('repository%3Aguaizai233%2Ffrostagent%3Apull', token_call.args[0])
                self.assertTrue(token_call.kwargs['headers']['Authorization'].startswith('Basic '))

    def test_missing_credentials_fail_before_network(self):
        with patch.object(gc, 'request_json') as request:
            for token, actor in [(None, 'actor'), ('token', None)]:
                with self.assertRaises(gc.CleanupError):
                    gc.GHCR(token, actor)
            request.assert_not_called()

    def test_pagination_reads_more_than_one_hundred_versions(self):
        client = self.client()
        pages = [[version(number) for number in range(1, 101)],
                 [version(number) for number in range(101, 201)], [version(201)]]
        with patch.object(client, 'api', side_effect=[(page, {}) for page in pages]) as api:
            self.assertEqual(len(client.versions()), 201)
        self.assertEqual(api.call_count, 3)
        self.assertIn('per_page=100&page=3&state=active', api.call_args.args[0])

    def test_full_page_followed_by_empty_page(self):
        client = self.client()
        with patch.object(client, 'api', side_effect=[([version(n) for n in range(1, 101)], {}), ([], {})]):
            self.assertEqual(len(client.versions()), 100)

    def test_short_page_with_next_link_is_not_treated_as_complete(self):
        client = self.client()
        headers = {'Link': '<https://api.github.com' + client.path + '?page=2&per_page=100>; rel="next"'}
        with patch.object(client, 'api', side_effect=[([version(1)], headers), ([version(2)], {})]) as api:
            self.assertEqual(len(client.versions()), 2)
            self.assertIn('page=2', api.call_args.args[0])

    def test_nonadvancing_pagination_fails_without_looping(self):
        client = self.client()
        for target in ('?page=1', '?page=3', '?page=invalid'):
            headers = {'Link': '<https://api.github.com' + client.path + target + '>; rel="next"'}
            with self.subTest(target=target), patch.object(client, 'api', return_value=([version(1)], headers)):
                with self.assertRaisesRegex(gc.CleanupError, 'pagination'):
                    client.versions()

    def test_pagination_error_does_not_return_partial_list(self):
        client = self.client()
        with patch.object(client, 'api', side_effect=[([version(n) for n in range(1, 101)], {}), gc.CleanupError('403')]):
            with self.assertRaises(gc.CleanupError):
                client.versions()

    def test_registry_digest_validation(self):
        client = self.client()
        for returned in ('', 'not-a-digest', digest(2)):
            with self.subTest(returned=returned), patch.object(gc, 'request_json', return_value=(leaf(), {'Docker-Content-Digest': returned})):
                with self.assertRaises(gc.CleanupError):
                    client.manifest(digest(1))
        with patch.object(gc, 'request_json', return_value=(leaf(), {'Docker-Content-Digest': digest(1)})):
            self.assertEqual(client.manifest(digest(1)), (digest(1), leaf()))

    def test_delete_targets_one_version_only(self):
        client = self.client()
        with patch.object(client, 'api') as api:
            client.delete(6)
            api.assert_called_once_with(client.path + '/6', method='DELETE')

    def test_http_errors_hide_credentials_and_response_body(self):
        error = HTTPError('https://example.invalid', 403, 'Forbidden', {}, io.BytesIO(b'synthetic-secret-body'))
        for failure in (error, URLError('synthetic-secret'), TimeoutError()):
            with self.subTest(failure=type(failure).__name__), patch.object(gc, 'urlopen', side_effect=failure):
                with self.assertRaises(gc.CleanupError) as caught:
                    gc.request_json('https://example.invalid', headers={'Authorization': 'synthetic-secret-token'})
                self.assertNotIn('synthetic-secret', str(caught.exception))

    def test_transport_json_empty_delete_and_timeout(self):
        for body, expected in [(b'{"ok": true}', {'ok': True}), (b'', None)]:
            response = io.BytesIO(body)
            response.headers = Message()
            with patch.object(gc, 'urlopen', return_value=response) as request:
                self.assertEqual(gc.request_json('https://example.invalid', headers={})[0], expected)
                self.assertEqual(request.call_args.kwargs['timeout'], 30)

    def test_cli_is_dry_run_by_default(self):
        with patch.object(gc, 'GHCR') as client, patch.object(gc, 'cleanup') as cleanup:
            self.assertEqual(gc.main([]), 0)
            cleanup.assert_called_once_with(client.return_value, apply=False)
        with patch.object(gc, 'GHCR') as client, patch.object(gc, 'cleanup') as cleanup:
            self.assertEqual(gc.main(['--apply']), 0)
            cleanup.assert_called_once_with(client.return_value, apply=True)

    def test_cli_failure_is_nonzero(self):
        with patch.object(gc, 'GHCR', side_effect=gc.CleanupError('HTTP 403')), patch('sys.stderr', new_callable=io.StringIO):
            self.assertEqual(gc.main(['--apply']), 1)

    def test_complete_client_flow_uses_rest_ids_and_keeps_registry_dependencies(self):
        registry = Registry()
        calls = []
        version_url = 'https://api.github.com/users/GuaiZai233/packages/container/frostagent/versions'

        def respond(url, *, headers, method='GET'):
            calls.append((method, url))
            if url == f'https://api.github.com/repos/{gc.REPOSITORY}':
                return {'owner': {'type': 'User'}}, {}
            if url.startswith('https://ghcr.io/token?'):
                return {'token': 'synthetic-pull'}, {}
            if url.startswith(version_url + '?'):
                return registry.versions(), {}
            if url.startswith(version_url + '/'):
                version_id = int(url.rsplit('/', 1)[1])
                if method == 'DELETE':
                    registry.delete(version_id)
                    return None, {}
                return registry.version(version_id), {}
            if '/manifests/' in url:
                value, manifest = registry.manifest(url.rsplit('/', 1)[1])
                return manifest, {'Docker-Content-Digest': value}
            self.fail(f'Unexpected request: {method} {url}')

        with patch.object(gc, 'request_json', side_effect=respond):
            gc.cleanup(gc.GHCR('synthetic-github', 'synthetic-actor'), apply=True, log=lambda _: None)
        self.assertEqual(registry.deleted, [7])
        self.assertEqual([url for method, url in calls if method == 'DELETE'],
                         [version_url + '/7'])


class WorkflowTests(unittest.TestCase):
    def test_publishing_and_cleanup_share_lock_and_never_cancel(self):
        workflow = (ROOT / '.github/workflows/docker.yml').read_text(encoding='utf-8')
        self.assertEqual(workflow.count('group: ghcr-frostagent-maintenance'), 2)
        self.assertEqual(workflow.count('queue: max'), 2)
        self.assertEqual(workflow.count('cancel-in-progress: false'), 2)
        self.assertIn('type=sha,prefix=sha-', workflow)
        self.assertIn('flavor: latest=false', workflow)
        self.assertIn('type=raw,value=latest,enable={{is_default_branch}}', workflow)
        self.assertIn('git ls-remote origin refs/heads/main', workflow)
        self.assertIn('default: true', workflow)
        self.assertIn("github.ref == 'refs/heads/main'", workflow)
        self.assertEqual(workflow.count("github.repository == 'GuaiZai233/FrostAgent'"), 2)
        self.assertGreater(workflow.index('- name: Clean up old GHCR images'), workflow.index('- name: Build and push image'))

    def test_retention_tests_run_in_ci(self):
        workflow = (ROOT / '.github/workflows/ci.yml').read_text(encoding='utf-8')
        self.assertIn("python3 -m unittest discover -s scripts -p 'test_ghcr_cleanup.py' -v", workflow)


if __name__ == '__main__':
    unittest.main()
