"""Resolve CI build identity without interpolating a ref into shell code."""

import os
import re
import subprocess
import sys


NUMBER = r'(?:0|[1-9][0-9]*)'
PRERELEASE_PART = r'(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)'
SEMVER = re.compile(
    rf'v({NUMBER}\.{NUMBER}\.{NUMBER}'
    rf'(?:-{PRERELEASE_PART}(?:\.{PRERELEASE_PART})*)?'
    r'(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?)\Z'
)
COMMIT = re.compile(r'(?:[0-9a-f]{40}|[0-9a-f]{64})\Z')


def resolve_metadata(ref, commit):
    """Only an explicitly triggered version tag is a versioned build."""
    if not COMMIT.fullmatch(commit):
        raise ValueError('build commit must be a full lowercase Git revision')
    version = ''
    if ref.startswith('refs/tags/'):
        match = SEMVER.fullmatch(ref.removeprefix('refs/tags/'))
        if match is None:
            raise ValueError('version tag must be vMAJOR.MINOR.PATCH with optional SemVer prerelease/build metadata')
        version = match.group(1)
    return {'version': version, 'commit': commit,
            'display_version': version or commit[:7]}


def main():
    # Use the checked-out commit, including the tested merge commit for PRs.
    commit = subprocess.check_output(
        ['git', 'rev-parse', '--verify', 'HEAD'], text=True, encoding='utf-8'
    ).strip()
    metadata = resolve_metadata(os.environ.get('GITHUB_REF', ''), commit)
    for key, value in metadata.items():
        print(f'{key}={value}')


if __name__ == '__main__':
    try:
        main()
    except (ValueError, subprocess.CalledProcessError) as error:
        print(f'Cannot resolve build identity: {error}', file=sys.stderr)
        sys.exit(1)
