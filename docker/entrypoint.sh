#!/bin/sh
set -eu
if [ "${1:-}" = update ]; then
    echo 'This GOX installation is managed by Docker.' >&2
    echo 'Pull a newer image and recreate the container (docker compose pull && docker compose up -d).' >&2
    exit 1
fi
export GOX_LSP_AUTO_UPDATE=0
exec /usr/local/bin/gox "$@"
