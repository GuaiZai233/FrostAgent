"""Keep releases, current main, and the ten most recent intermediate commit images.

Dry-run by default. All publishers and cleanup jobs must share the workflow's
ghcr-frostagent-maintenance concurrency group. No third-party packages required.
"""

import argparse
import base64
import json
import os
import re
import sys
from datetime import datetime
from urllib.error import HTTPError, URLError
from urllib.parse import parse_qs, quote, urlencode, urlsplit
from urllib.request import Request, urlopen


REPOSITORY = 'GuaiZai233/FrostAgent'
RELEASE_TAG = re.compile(r'v?\d+\.\d+\.\d+(?:[-_][0-9A-Za-z_.-]+)?\Z')
SHA_TAG = re.compile(r'sha-[0-9a-f]{7,40}\Z')
INTERMEDIATE_RETENTION = 10
DIGEST = re.compile(r'sha256:[0-9a-f]{64}\Z')
INDEX_TYPES = {
    'application/vnd.oci.image.index.v1+json',
    'application/vnd.docker.distribution.manifest.list.v2+json',
}
IMAGE_TYPES = {
    'application/vnd.oci.image.manifest.v1+json',
    'application/vnd.docker.distribution.manifest.v2+json',
}


class CleanupError(RuntimeError):
    """An unsafe or incomplete cleanup plan must not be executed."""


def request_json(url, *, headers, method='GET'):
    request = Request(url, headers=headers, method=method)
    try:
        with urlopen(request, timeout=30) as response:
            body = response.read()
            return (json.loads(body) if body else None), response.headers
    except HTTPError as error:
        # Do not print request headers, credentials, or untrusted response bodies.
        raise CleanupError(f'{method} {url}: HTTP {error.code}') from None
    except (URLError, TimeoutError, ValueError) as error:
        raise CleanupError(f'{method} {url}: {type(error).__name__}') from None


def version_key(version):
    """Validate API fields before making any retention decision."""
    try:
        version_id = version['id']
        digest = version['name']
        tags = version['metadata']['container']['tags']
        created_at_text = version['created_at']
        if (type(version_id) is not int or version_id <= 0
                or not isinstance(digest, str) or not DIGEST.fullmatch(digest)
                or not isinstance(tags, list)
                or any(not isinstance(tag, str) or not tag for tag in tags)
                or not isinstance(created_at_text, str)):
            raise ValueError
        created_at = datetime.fromisoformat(created_at_text.replace('Z', '+00:00'))
        if created_at.tzinfo is None:
            raise ValueError
        return version_id, digest, tuple(sorted(tags)), created_at
    except (KeyError, TypeError, ValueError):
        raise CleanupError('Invalid container version metadata; refusing cleanup') from None


def snapshot(versions):
    keys = tuple(sorted(version_key(version) for version in versions))
    if (len({key[0] for key in keys}) != len(keys)
            or len({key[1] for key in keys}) != len(keys)):
        raise CleanupError('Duplicate container versions; refusing cleanup')
    return keys


def protected_tag(tag):
    return tag in {'main', 'latest'} or RELEASE_TAG.fullmatch(tag) is not None


def reconcile_registry_tags(client, api_tags):
    """Fail closed unless GHCR and Packages API report the same complete tag set."""
    registry_tags = client.registry_tags()
    if (not isinstance(registry_tags, (tuple, list))
            or any(not isinstance(tag, str) or not tag for tag in registry_tags)
            or len(registry_tags) != len(set(registry_tags))):
        raise CleanupError('Invalid registry tag set; refusing cleanup')
    registry_set = set(registry_tags)
    api_set = set(api_tags)
    if registry_set != api_set:
        registry_only = len(registry_set - api_set)
        metadata_only = len(api_set - registry_set)
        raise CleanupError(
            'Registry tag list and Packages API metadata disagree '
            f'(registry-only={registry_only}, metadata-only={metadata_only}); refusing cleanup'
        )


def manifest_references(manifest):
    """Walk indexes (including BuildKit attestations) and manifest subjects."""
    if not isinstance(manifest, dict) or manifest.get('schemaVersion') != 2:
        raise CleanupError('Invalid manifest schema; refusing cleanup')
    media_type = manifest.get('mediaType')
    if media_type in INDEX_TYPES:
        descriptors = manifest.get('manifests')
        if not isinstance(descriptors, list) or not descriptors:
            raise CleanupError('Invalid or empty image index; refusing cleanup')
    elif media_type in IMAGE_TYPES:
        if not isinstance(manifest.get('config'), dict) or not isinstance(manifest.get('layers'), list):
            raise CleanupError('Invalid image manifest; refusing cleanup')
        descriptors = []
    else:
        raise CleanupError('Unsupported manifest media type; refusing cleanup')
    if 'subject' in manifest:
        descriptors = [*descriptors, manifest['subject']]
    references = []
    for descriptor in descriptors:
        digest = descriptor.get('digest') if isinstance(descriptor, dict) else None
        if not isinstance(digest, str) or not DIGEST.fullmatch(digest):
            raise CleanupError('Invalid manifest descriptor; refusing cleanup')
        references.append(digest)
    return references


class GHCR:
    def __init__(self, token, actor):
        if not token or not actor:
            raise CleanupError('GH_TOKEN and GITHUB_ACTOR are required')
        self.api_headers = {
            'Authorization': f'Bearer {token}',
            'Accept': 'application/vnd.github+json',
            'X-GitHub-Api-Version': '2022-11-28',
        }
        repository, _ = self.api(f'/repos/{REPOSITORY}')
        owner_type = repository['owner']['type']
        if owner_type not in {'User', 'Organization'}:
            raise CleanupError('Unsupported package owner type')
        owner, package = REPOSITORY.split('/')
        scope = 'users' if owner_type == 'User' else 'orgs'
        self.path = f'/{scope}/{owner}/packages/container/{quote(package.lower(), safe="")}/versions'
        credentials = base64.b64encode(f'{actor}:{token}'.encode()).decode()
        query = urlencode({'service': 'ghcr.io', 'scope': f'repository:{REPOSITORY.lower()}:pull'})
        auth, _ = request_json(f'https://ghcr.io/token?{query}',
                               headers={'Authorization': f'Basic {credentials}'})
        if not isinstance(auth, dict) or not auth.get('token'):
            raise CleanupError('GHCR did not return a pull token')
        self.registry_headers = {
            'Authorization': f'Bearer {auth["token"]}',
            'Accept': ', '.join(sorted(INDEX_TYPES | IMAGE_TYPES)),
        }

    def api(self, path, method='GET'):
        return request_json(f'https://api.github.com{path}', headers=self.api_headers, method=method)

    def versions(self):
        versions = []
        page = 1
        while True:
            batch, headers = self.api(f'{self.path}?per_page=100&page={page}&state=active')
            if not isinstance(batch, list):
                raise CleanupError('Invalid package version list')
            versions.extend(batch)
            snapshot(versions)
            next_link = re.search(r'<([^>]+)>;\s*rel="next"', headers.get('Link', ''))
            if next_link:
                # Follow pagination even on a short page, but never forward the
                # bearer token to a URL from a response. Rebuild our own URL.
                next_page = parse_qs(urlsplit(next_link.group(1)).query).get('page')
                if next_page != [str(page + 1)]:
                    raise CleanupError('Unexpected package pagination; refusing cleanup')
            elif len(batch) < 100:
                return versions
            page += 1

    def registry_tags(self):
        """Read every GHCR tag page without trusting links to other hosts."""
        path = f'/v2/{REPOSITORY.lower()}/tags/list'
        url = f'https://ghcr.io{path}?n=100'
        visited = set()
        tags = []
        while True:
            if url in visited:
                raise CleanupError('Repeated registry pagination; refusing cleanup')
            visited.add(url)
            payload, headers = request_json(url, headers=self.registry_headers)
            if (not isinstance(payload, dict)
                    or payload.get('name') != REPOSITORY.lower()
                    or not isinstance(payload.get('tags'), list)):
                raise CleanupError('Invalid registry tag listing; refusing cleanup')
            batch = payload['tags']
            if any(not isinstance(tag, str) or not tag for tag in batch):
                raise CleanupError('Invalid registry tag; refusing cleanup')
            tags.extend(batch)

            next_link = re.search(r'<([^>]+)>;\s*rel="next"', headers.get('Link', ''))
            if not next_link:
                if len(tags) != len(set(tags)):
                    raise CleanupError('Duplicate registry tags; refusing cleanup')
                return tuple(sorted(tags))

            target = urlsplit(next_link.group(1))
            if (target.scheme != 'https' or target.netloc != 'ghcr.io'
                    or target.path != path or not target.query
                    or target.username is not None or target.password is not None
                    or target.fragment):
                raise CleanupError('Unexpected registry pagination URL; refusing cleanup')
            # Rebuild the URL with the validated fixed host/path so the registry
            # bearer token can never be sent to a URL supplied by a response.
            url = f'https://ghcr.io{path}?{target.query}'

    def version(self, version_id):
        version, _ = self.api(f'{self.path}/{version_id}')
        return version

    def manifest(self, reference):
        url = f'https://ghcr.io/v2/{REPOSITORY.lower()}/manifests/{quote(reference, safe=":")}'
        manifest, headers = request_json(url, headers=self.registry_headers)
        digest = headers.get('Docker-Content-Digest', '')
        if not DIGEST.fullmatch(digest) or (DIGEST.fullmatch(reference) and reference != digest):
            raise CleanupError('Missing or mismatched registry digest; refusing cleanup')
        return digest, manifest

    def delete(self, version_id):
        self.api(f'{self.path}/{version_id}', method='DELETE')


def cleanup(client, *, apply=False, log=print):
    versions = client.versions()
    original = snapshot(versions)
    tags = {}
    for _, digest, names, _ in original:
        for tag in names:
            if tag in tags:
                raise CleanupError('Duplicate image tags; refusing cleanup')
            tags[tag] = digest

    # The Packages API and registry can briefly disagree after a push/retag.
    # Package-version deletion removes every alias on that version, so never
    # delete anything unless both APIs agree on the complete tag-name set.
    reconcile_registry_tags(client, tags)

    if 'main' not in tags or tags.get('latest') != tags['main']:
        raise CleanupError('main and latest must point to the same image; refusing cleanup')

    # Keep the ten most recent SHA-tagged intermediate images. The current
    # main/latest image and release-tagged images are protected separately and
    # do not consume the ten-image allowance.
    intermediate = []
    for version in versions:
        version_id, digest, names, created_at = version_key(version)
        sha_tags = tuple(tag for tag in names if SHA_TAG.fullmatch(tag))
        if sha_tags and not any(protected_tag(tag) for tag in names):
            intermediate.append((created_at, version_id, digest, sha_tags))
    intermediate.sort(reverse=True)
    retained_sha_tags = {
        tag
        for _, _, _, sha_tags in intermediate[:INTERMEDIATE_RETENTION]
        for tag in sha_tags
    }

    # Resolve all protected tags and selected historical commit tags before the
    # first DELETE. Validate their registry digests and preserve dependencies.
    pending = []
    root_tags = {tag for tag in tags if protected_tag(tag)} | retained_sha_tags
    for tag in sorted(root_tags):
        expected = tags[tag]
        digest, manifest = client.manifest(tag)
        if digest != expected:
            raise CleanupError(f'Tag {tag} changed or is not yet consistent; refusing cleanup')
        pending.append((digest, manifest))
    keep = set()
    while pending:
        digest, manifest = pending.pop()
        if digest in keep:
            continue
        keep.add(digest)
        for child in manifest_references(manifest):
            if child not in keep:
                child_digest, child_manifest = client.manifest(child)
                if child_digest != child:
                    raise CleanupError('Dependency digest mismatch; refusing cleanup')
                pending.append((child, child_manifest))

    candidates = [version for version in versions if version['name'] not in keep]
    # Remove tagged roots before orphaned manifests. Retained shared children
    # are never candidates, even when they carry an old sha-* tag themselves.
    candidates.sort(key=lambda version: not bool(version['metadata']['container']['tags']))
    log(f'{"APPLY" if apply else "DRY RUN"}: keep {len(versions) - len(candidates)}, delete {len(candidates)} versions')
    for version in candidates:
        version_id, digest, names, _ = version_key(version)
        log(f'Candidate {version_id}: {digest} tags={json.dumps(names)}')
    if not apply:
        return candidates

    if snapshot(client.versions()) != original:
        raise CleanupError('Package changed during planning; refusing cleanup')
    # Reconcile again at the last safe point before the first destructive call.
    reconcile_registry_tags(client, tags)
    for version in candidates:
        version_id, _, _, _ = version_key(version)
        if version_key(client.version(version_id)) != version_key(version):
            raise CleanupError(f'Version {version_id} changed before deletion; stopping cleanup')
        client.delete(version_id)
        log(f'Deleted version {version_id}')
    return candidates


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--apply', action='store_true', help='delete planned versions (default: dry-run)')
    args = parser.parse_args(argv)
    try:
        cleanup(GHCR(os.environ.get('GH_TOKEN'), os.environ.get('GITHUB_ACTOR')), apply=args.apply)
    except (CleanupError, KeyError, TypeError) as error:
        print(f'GHCR cleanup failed: {error}', file=sys.stderr)
        return 1
    return 0


if __name__ == '__main__':
    sys.exit(main())
