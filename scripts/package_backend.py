"""Build matching backend/CLI bundles using the checked-out source identity."""

import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile
import zipfile

from build_metadata import resolve_metadata


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--output', default='bin/packages')
    parser.add_argument('--targets', nargs='+', default=['linux/amd64', 'windows/amd64'])
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    commit = subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=root, text=True).strip()
    metadata = resolve_metadata(os.environ.get('GITHUB_REF', ''), commit)
    output = (root / args.output).resolve()
    output.mkdir(parents=True, exist_ok=True)
    flags = ('-s -w -X github.com/GuaiZai233/FrostAgent/internal/buildinfo.Commit=' + commit)
    if metadata['version']:
        flags += ' -X github.com/GuaiZai233/FrostAgent/internal/buildinfo.Version=' + metadata['version']
    for target in args.targets:
        if target not in ('linux/amd64', 'windows/amd64', 'linux/arm64'):
            raise ValueError('unsupported target: ' + target)
        system, arch = target.split('/')
        extension = '.exe' if system == 'windows' else ''
        with tempfile.TemporaryDirectory(prefix='frostagent-package-') as temporary:
            stage = Path(temporary)
            env = {**os.environ, 'GOOS': system, 'GOARCH': arch, 'CGO_ENABLED': '0'}
            for name, package in [('frostagent', './cmd/app'), ('frostctl', './cmd/frostctl')]:
                subprocess.run(['go', 'build', '-trimpath', '-ldflags', flags,
                                '-o', str(stage / (name + extension)), package],
                               cwd=root, env=env, check=True)
            archive = output / ('frostagent-' + metadata['display_version'] + '-' + system + '-' + arch + '.zip')
            with zipfile.ZipFile(archive, 'w', zipfile.ZIP_DEFLATED) as bundle:
                for binary in sorted(stage.iterdir()):
                    bundle.write(binary, binary.name)
                bundle.write(root / 'docs/frostctl.md', 'frostctl.md')
                bundle.write(root / 'THIRD_PARTY_NOTICES.md', 'THIRD_PARTY_NOTICES.md')
                bundle.writestr('build.json', json.dumps({**metadata, 'target': target}, indent=2))
            print(archive)


if __name__ == '__main__':
    main()
