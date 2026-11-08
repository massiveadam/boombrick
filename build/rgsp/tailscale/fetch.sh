#!/bin/sh
# Fetches Tailscale's static Linux build, checked against the SHA-256 that
# pkgs.tailscale.com publishes for it, and unpacks tailscale and tailscaled
# into dist/tailscale-ARCH.   fetch.sh [arm64|amd64]
set -eu
ROOT=$(cd "$(dirname "$0")/../../.." && pwd)
ARCH=${1:-arm64}
VER=1.104.5
case $ARCH in
arm64) SHA=e900b0e6e25023a1e504b2c237ce2a32ea0535e006f8c1a1097a490886493b6c ;;
amd64) SHA=720cbbccbfc03d8b55df72afc19e5a72e31b0f65934700aa6fa38074d4ad7bf7 ;;
*) echo "arch arm64 or amd64" >&2; exit 2 ;;
esac
DL=$ROOT/dist/rgsp-dl; OUT=$ROOT/dist/tailscale-$ARCH
mkdir -p "$DL"
f=$DL/tailscale_${VER}_$ARCH.tgz
[ -f "$f" ] || curl -sSfL -o "$f" "https://pkgs.tailscale.com/stable/tailscale_${VER}_$ARCH.tgz"
echo "$SHA  $f" | sha256sum -c --quiet
rm -rf "$OUT"; mkdir -p "$OUT"
tar -xzf "$f" -C "$OUT" --strip-components=1 "tailscale_${VER}_$ARCH/tailscale" "tailscale_${VER}_$ARCH/tailscaled"
# BSD-3-Clause; the tarball carries no copy, so the release tag's.
lic=$DL/tailscale-$VER-LICENSE
[ -f "$lic" ] || curl -sSfL -o "$lic" "https://raw.githubusercontent.com/tailscale/tailscale/v$VER/LICENSE"
cp "$lic" "$OUT/LICENSE"
echo "$OUT"
