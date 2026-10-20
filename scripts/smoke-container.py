#!/usr/bin/env python3
"""Verify a GOX container can compile plugins and serve a fresh project."""
import argparse
import subprocess
import time
import urllib.request
import uuid


def run(*args):
    return subprocess.check_output(["docker", *args], text=True, timeout=600).strip()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("image")
    args = parser.parse_args()
    prefix = "gox-smoke-" + uuid.uuid4().hex[:12]
    projects, cache = prefix + "-projects", prefix + "-cache"
    container = prefix + "-server"
    try:
        run("volume", "create", projects)
        run("volume", "create", cache)
        mounts = ["-v", projects + ":/workspace", "-v", cache + ":/var/lib/gox"]
        version = run("run", "--rm", args.image, "version")
        if "GOX Server v0.1.3" not in version:
            raise RuntimeError("Unexpected GOX release: " + version)
        if run("run", "--rm", "--entrypoint", "id", args.image, "-u") == "0":
            raise RuntimeError("The default container user must not be root")
        update = subprocess.run(["docker", "run", "--rm", args.image, "update"],
                                text=True, capture_output=True, timeout=30)
        if update.returncode != 1 or "managed by Docker" not in update.stderr:
            raise RuntimeError("Container must redirect updates to Docker")
        run("run", "--rm", *mounts, args.image, "createproject", "my-project")
        run("run", "-d", "--name", container, "-p", "127.0.0.1::3000",
            *mounts, "-w", "/workspace/my-project", args.image,
            "run", "--host", "0.0.0.0", "--port", "3000")
        port = run("port", container, "3000/tcp").rsplit(":", 1)[1]
        deadline = time.monotonic() + 600
        while time.monotonic() < deadline:
            try:
                with urllib.request.urlopen("http://127.0.0.1:" + port, timeout=5) as response:
                    body = response.read().decode()
                    if response.status == 200 and "Hello Gox" in body:
                        print("Container smoke test passed")
                        return
            except (OSError, ValueError):
                pass
            if run("inspect", "-f", "{{.State.Running}}", container) != "true":
                raise RuntimeError("GOX container exited")
            time.sleep(1)
        raise RuntimeError("Timed out waiting for the GOX page")
    except Exception:
        subprocess.run(["docker", "logs", container], check=False)
        raise
    finally:
        subprocess.run(["docker", "rm", "-f", container], check=False, capture_output=True)
        for volume in (projects, cache):
            subprocess.run(["docker", "volume", "rm", volume], check=False, capture_output=True)


if __name__ == "__main__":
    main()
