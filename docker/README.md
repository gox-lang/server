# GOX container preparation

The image downloads GOX 0.1.3 from the official GitHub Release and checks SHA-256
before extracting the executable. It does not compile GOX or accept another binary
source. The intended registry is `ghcr.io/gox-lang/server`; publication is a separate
step after the native Linux amd64 and arm64 validation jobs pass.

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

## Local trial before publication

For an existing GOX project, copy [compose.yaml](compose.yaml) into its root. With
the locally built image, run:

```sh
GOX_IMAGE=gox-container-test docker compose up
```

The page is served at `http://localhost:3000`. Project files are bind-mounted from
the host; managed toolchains persist in the named volume. The first run needs
internet access to provision the Go/Node toolchains and project dependencies.
The image runs with UID/GID 1000. On Linux, the mounted project must be writable
by that user; users with different IDs need a matching ownership/user configuration.

`docker compose down` removes containers but retains the toolchain volume.
`docker compose down -v` also removes that cache. Project source remains on the host.

GOX updates belong to the image: replace the image and recreate the container.
The entrypoint redirects `gox update` and disables LSP auto-update. `/usr/local/bin/gox`
is root-owned and cannot be replaced by the default unprivileged user.

This is a development image with a C compiler and build tools, not a minimal
production runtime. The public website should advertise it only after publication
and an anonymous pull have been verified.
