#!/usr/bin/env python3
"""Exercise config and page plugins using the actual release executable."""
from pathlib import Path
import argparse
import os
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('binary')
parser.add_argument('--installed-with-go', action='store_true', help='Use the same module cache as a local go install')
args = parser.parse_args()
binary = str(Path(args.binary).resolve())
with tempfile.TemporaryDirectory(prefix='gox-release-') as directory:
    root = Path(directory)
    # Downloaded releases must work independently of the builder’s module cache.
    # A local go install uses the user’s module cache for both host and plugins.
    env = dict(os.environ, GOCACHE=str(root / 'build-cache'))
    if not args.installed_with_go:
        env['GOMODCACHE'] = str(root / 'modules')
    subprocess.run([binary, 'createproject', 'smoke'], cwd=root, env=env, check=True, timeout=600)
    project = root / 'smoke'
    (project / 'pages/index.gox').write_text('''module index
import gox "github.com/gox-lang/gox"
func GET(ctx *gox.Context) gox.Page {
    return <h1>GOX release plugin OK</h1>
}
''')
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        port = sock.getsockname()[1]
    with tempfile.TemporaryFile(mode='w+b') as log:
        process = subprocess.Popen([binary, 'run', '--host', '127.0.0.1', '--port', str(port)], cwd=project, env=env, stdout=log, stderr=log)
        try:
            deadline = time.monotonic() + 300
            while time.monotonic() < deadline:
                if process.poll() is not None:
                    raise RuntimeError('GOX exited before serving the page')
                try:
                    with urllib.request.urlopen(f'http://127.0.0.1:{port}/', timeout=10) as response:
                        assert b'GOX release plugin OK' in response.read()
                    print('Release config and page plugins passed')
                    break
                except (OSError, TimeoutError):
                    time.sleep(1)
            else:
                raise RuntimeError('GOX did not serve the test page')
        except BaseException:
            log.seek(0)
            print(log.read().decode(errors='replace'), file=sys.stderr)
            raise
        finally:
            process.terminate()
            try:
                process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()
