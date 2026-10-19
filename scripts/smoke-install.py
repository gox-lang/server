#!/usr/bin/env python3
"""Verify Go's default executable name without an explicit build -o override."""
import argparse
import os
from pathlib import Path
import subprocess
import tempfile
import sys

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--project', action='store_true', help='Also exercise runtime plugins in an isolated project')
args = parser.parse_args()
root = Path(__file__).resolve().parent.parent
with tempfile.TemporaryDirectory(prefix='gox-install-') as directory:
    env = dict(os.environ, GOBIN=directory)
    subprocess.run(['go', 'install', './cmd/gox'], cwd=root, env=env, check=True)
    binary = Path(directory) / ('gox.exe' if os.name == 'nt' else 'gox')
    if not binary.is_file():
        raise RuntimeError('go install did not produce gox')
    output = subprocess.check_output([str(binary), 'version'], text=True)
    if not output.startswith('GOX Server '):
        raise RuntimeError(f'Unexpected version output: {output}')
    subprocess.run([str(binary), '--help'], check=True, stdout=subprocess.DEVNULL)
    print(f'Go installation passed: {binary.name}: {output.strip()}')
    if args.project:
        subprocess.run([sys.executable, str(root / 'scripts/smoke-project.py'), str(binary), '--installed-with-go'], check=True)
