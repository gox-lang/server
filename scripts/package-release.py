#!/usr/bin/env python3
"""Build and package GOX on the native release runner."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import tarfile
import zipfile


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('version')
    parser.add_argument('--output', default='dist')
    args = parser.parse_args()
    if not re.fullmatch(r'v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?', args.version):
        parser.error('version must be a v-prefixed semantic version')
    root = Path(__file__).resolve().parent.parent
    platform = json.loads(subprocess.check_output(['go', 'env', '-json', 'GOOS', 'GOARCH', 'GOHOSTOS', 'GOHOSTARCH'], cwd=root))
    system, arch = platform['GOOS'], platform['GOARCH']
    if system != platform['GOHOSTOS'] or arch != platform['GOHOSTARCH']:
        parser.error('release builds require a native runner')
    if system not in ('linux', 'darwin', 'windows') or arch not in ('amd64', 'arm64'):
        parser.error('unsupported release target')
    destination = Path(args.output).resolve()
    destination.mkdir(parents=True, exist_ok=True)
    name = f'gox_{args.version[1:]}_{system}_{arch}'
    staging = destination / name
    staging.mkdir(exist_ok=True)
    binary = staging / ('gox.exe' if system == 'windows' else 'gox')
    env = dict(os.environ, CGO_ENABLED='0' if system == 'windows' else '1', GOTOOLCHAIN='local')
    # Do not use -trimpath: runtime plugins must use compatible package build flags.
    subprocess.run(['go', 'build', '-ldflags', f'-s -w -X github.com/gox-lang/server/internal/buildinfo.GoxVersion={args.version}', '-o', str(binary), '.'], cwd=root, env=env, check=True)
    subprocess.run([str(binary), 'version'], check=True)
    license_file = root / 'LICENSE'
    archive = destination / (name + ('.zip' if system == 'windows' else '.tar.gz'))
    if system == 'windows':
        with zipfile.ZipFile(archive, 'w', zipfile.ZIP_DEFLATED) as out:
            out.write(binary, binary.name)
            out.write(license_file, 'LICENSE')
    else:
        with tarfile.open(archive, 'w:gz') as out:
            out.add(binary, arcname=binary.name)
            out.add(license_file, arcname='LICENSE')
    checksum = hashlib.sha256(archive.read_bytes()).hexdigest()
    archive.with_name(archive.name + '.sha256').write_text(f'{checksum}  {archive.name}\n')
    print(archive)


if __name__ == '__main__':
    main()
