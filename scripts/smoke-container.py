#!/usr/bin/env python3
"""Verify release containers, bind-mount ownership and isolated user caches."""
import argparse
import os
from pathlib import Path
import subprocess
import tempfile
import time
import urllib.request
import uuid


def run(*args):
    return subprocess.check_output(["docker", *args], text=True, timeout=600).strip()


def scenario(image, cache, uid, gid, version):
    container = "gox-smoke-" + uuid.uuid4().hex[:12]
    with tempfile.TemporaryDirectory(prefix="gox-bind-") as workspace:
        mounts = ["-v", workspace + ":/workspace", "-v", cache + ":/var/lib/gox"]
        user = ["--user", f"{uid}:{gid}"]
        try:
            # Only the isolated test directory is assigned to the simulated host user.
            run("run", "--rm", "--user", "0", "-v", workspace + ":/workspace",
                "--entrypoint", "chown", image, f"{uid}:{gid}", "/workspace")
            output = run("run", "--rm", *user, *mounts, image, "version")
            if f"GOX Server v{version}" not in output:
                raise RuntimeError("Unexpected release: " + output)
            run("run", "--rm", *user, *mounts, image, "createproject", "my-project")
            run("run", "-d", "--name", container, "-p", "127.0.0.1::3000",
                *user, *mounts, "-w", "/workspace/my-project", image,
                "run", "--host", "0.0.0.0", "--port", "3000")
            port = run("port", container, "3000/tcp").rsplit(":", 1)[1]
            deadline = time.monotonic() + 600
            while time.monotonic() < deadline:
                try:
                    with urllib.request.urlopen("http://127.0.0.1:" + port, timeout=5) as response:
                        if response.status == 200 and "Hello Gox" in response.read().decode():
                            break
                except (OSError, ValueError):
                    pass
                if run("inspect", "-f", "{{.State.Running}}", container) != "true":
                    raise RuntimeError("GOX container exited")
                time.sleep(1)
            else:
                raise RuntimeError("Timed out waiting for the GOX page")
            # Stop before auditing files so no writes race with ownership checks.
            run("stop", container)
            owners = run("run", "--rm", *user, *mounts, "--entrypoint", "find", image,
                         "/workspace", "-printf", "%U:%G\n")
            if set(owners.splitlines()) != {f"{uid}:{gid}"}:
                raise RuntimeError("Project files were created with incorrect ownership")
            state = f"/var/lib/gox/user-{uid}-{gid}"
            mode = run("run", "--rm", *user, *mounts, "--entrypoint", "stat", image,
                       "-c", "%u:%g:%a", state)
            if mode != f"{uid}:{gid}:700":
                raise RuntimeError("User cache permissions are incorrect: " + mode)
            run("run", "--rm", *user, *mounts, image, "version")
            print(f"Bind mount, plugin execution and persistent cache passed for {uid}:{gid}", flush=True)
        except Exception:
            subprocess.run(["docker", "logs", container], check=False)
            raise
        finally:
            subprocess.run(["docker", "rm", "-f", container], check=False, capture_output=True)
            # Restore only this temporary fixture for host-side cleanup.
            run("run", "--rm", "--user", "0", "-v", workspace + ":/workspace",
                "--entrypoint", "chown", image, "-R", f"{os.getuid()}:{os.getgid()}", "/workspace")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("image")
    parser.add_argument("--version", default="0.1.3")
    args = parser.parse_args()
    cache = "gox-smoke-cache-" + uuid.uuid4().hex[:12]
    try:
        run("volume", "create", cache)
        if run("run", "--rm", "--entrypoint", "id", args.image, "-u") == "0":
            raise RuntimeError("Default user must not be root")
        update = subprocess.run(["docker", "run", "--rm", args.image, "update"],
                                text=True, capture_output=True, timeout=30)
        if update.returncode != 1 or "managed by Docker" not in update.stderr:
            raise RuntimeError("Updates must be managed by Docker")
        for uid, gid in ((1000, 1000), (12345, 23456)):
            scenario(args.image, cache, uid, gid, args.version)
        print("Container smoke test passed", flush=True)
    finally:
        subprocess.run(["docker", "volume", "rm", cache], check=False, capture_output=True)


if __name__ == "__main__":
    main()
