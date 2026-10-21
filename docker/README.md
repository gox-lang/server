# GOX Docker image

The image downloads GOX 0.1.3 from the official GitHub Release and checks SHA-256
before extracting the executable. It does not compile GOX or accept another binary
source. The public image is available at `ghcr.io/gox-lang/server` for Linux amd64 and
arm64. Both `0.1.3` and `latest` are published, and anonymous downloading has been
verified. No registry login is required.

```sh
docker run --rm ghcr.io/gox-lang/server:latest version
```

## Maintainer validation

```sh
docker buildx build --load --platform linux/amd64 -t gox-container-test .
python3 scripts/smoke-container.py gox-container-test
GOX_IMAGE=gox-container-test docker compose -f docker/compose.yaml config --quiet
```

Run from the repository root. On an ARM64 host, use `linux/arm64`. The tests create
isolated Docker volumes, create a project, compile its plugins, request the default
page and remove their containers and volumes afterward. Python is used only by this
repository's CI check, never by the image or the user installing GOX.

## Run a project

For an existing GOX project, copy [compose.yaml](compose.yaml) into its root. Then run from that directory in a Linux, macOS or WSL shell:

```sh
GOX_UID=$(id -u) GOX_GID=$(id -g) docker compose up
```

The page is served at `http://localhost:3000`. Project files are bind-mounted from
the host; managed toolchains persist in the named volume. The first run needs
internet access to provision the Go/Node toolchains and project dependencies.
The image defaults to UID/GID 1000 and also supports arbitrary numeric users without
running GOX as root. The command above passes your host IDs so generated files belong to your user.

For repeated use, export GOX_UID and GOX_GID in your shell, or add their numeric
values to the project's local Compose environment file. Compose does not execute
shell substitutions inside YAML or environment files.

With plain Docker, add `--user "$(id -u):$(id -g)"`. The bind-mounted folder must be
writable by that user. The entrypoint never changes ownership of project files.
Each UID/GID pair gets a private directory below the toolchain volume, including
its HOME and GOX_HOME. Different users can reuse the volume without sharing writable
toolchains. Fresh volumes inherit the sticky parent permissions from the image;
volumes made with earlier experimental images may need to be recreated.

Docker Desktop on macOS uses the same Linux images (amd64 on Intel, arm64 on Apple
Silicon). The native Linux tests do not replace a Docker Desktop file-sharing test.

`docker compose down` removes containers but retains the toolchain volume.
`docker compose down -v` also removes that cache. Project source remains on the host.

The Compose example uses `latest`. It does not automatically update running
containers. Download the current image and recreate the container with:

```sh
docker compose pull
GOX_UID=$(id -u) GOX_GID=$(id -g) docker compose up --force-recreate
```

To keep a specific version, use a fixed tag such as `ghcr.io/gox-lang/server:0.1.3`.
For a local development build, set `GOX_IMAGE=gox-container-test`.
GOX updates belong to the image: replace the image and recreate the container.
The entrypoint redirects `gox update` and disables LSP auto-update. `/usr/local/bin/gox`
is root-owned and cannot be replaced by the default unprivileged user.

This is a development image with a C compiler and build tools, not a minimal
production runtime.

## Publication

The manual **Publish container image** GitHub Actions workflow accepts an existing
release version (without `v`). It builds and tests both architectures natively,
pushes each verified image, then publishes the combined version tag only after both
jobs succeed. The optional latest checkbox explicitly updates the rolling tag.
It uses the repository GITHUB_TOKEN with packages:write; no Docker Hub account or
personal access token is required.

After the first publication, check the package visibility in GitHub Packages and
make it public if necessary. Verify an anonymous pull before advertising it on the
website. The initial 0.1.3 publication and anonymous pull verification are complete.
