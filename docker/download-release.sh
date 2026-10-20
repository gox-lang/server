#!/bin/sh
# Only official, versioned GitHub Releases may supply the GOX executable.
set -eu
version=${1:?Supply a release version}
arch=${2:?Supply amd64 or arm64}
out=${3:?Supply an output directory}
printf '%s\n' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' || exit 1
case "$arch" in amd64|arm64) ;; *) echo 'Unsupported architecture' >&2; exit 1;; esac
archive="gox_${version}_linux_${arch}.tar.gz"
base="https://github.com/gox-lang/server/releases/download/v${version}"
temp=$(mktemp -d)
trap 'rm -rf "$temp"' EXIT
trap 'exit 1' HUP INT TERM
curl -fSL --proto '=https' --tlsv1.2 --retry 3 --connect-timeout 20 --max-time 300 "$base/$archive" -o "$temp/$archive"
curl -fSL --proto '=https' --tlsv1.2 --retry 3 --connect-timeout 20 --max-time 60 "$base/checksums.sha256" -o "$temp/checksums.sha256"
expected=$(awk -v name="$archive" '$2 == name {print $1; count++} END {if (count != 1) exit 1}' "$temp/checksums.sha256")
printf '%s\n' "$expected" | grep -Eq '^[a-fA-F0-9]{64}$'
(cd "$temp" && printf '%s  %s\n' "$expected" "$archive" | sha256sum -c -)
mkdir -p "$out"
tar -xzf "$temp/$archive" -C "$out" gox LICENSE
[ -f "$out/gox" ] && [ ! -L "$out/gox" ]
[ -f "$out/LICENSE" ] && [ ! -L "$out/LICENSE" ]
chmod 755 "$out/gox"
