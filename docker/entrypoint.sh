#!/bin/sh
set -eu
if [ "${1:-}" = update ]; then
    echo 'This GOX installation is managed by Docker.' >&2
    echo 'Pull a newer image and recreate the container (docker compose pull && docker compose up -d).' >&2
    exit 1
fi
# A sticky shared parent permits arbitrary numeric users, with private per-user caches.
uid=$(id -u)
gid=$(id -g)
state="/var/lib/gox/user-${uid}-${gid}"
umask 077
mkdir -p "$state"
if [ -L "$state" ] || [ "$(stat -c %u "$state")" != "$uid" ]; then
    echo 'GOX cache directory must be owned by the container user and must not be a symlink.' >&2
    exit 1
fi
chmod 700 "$state"
export HOME="$state/home" GOX_HOME="$state/toolchains" GOX_LSP_AUTO_UPDATE=0
mkdir -p "$HOME" "$GOX_HOME"
umask 022
exec /usr/local/bin/gox "$@"
