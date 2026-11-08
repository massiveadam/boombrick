#!/usr/bin/env bash
# Makes the C headers Boombrick's desktop build needs available without
# installing anything system-wide: downloads the Ubuntu -dev packages for
# SDL2, ALSA, Opus, Ogg and X11 and unpacks them into a folder of your own,
# pointing their libraries at the ones already installed. Prints the env
# file to source before `go build`.
#
#   scripts/devroot.sh [DIR]      (default ../sp-firmwaremod-upstream/.devroot)
#
# The alternative is upstream's scripts/setup-wsl.sh, which uses sudo apt.
set -euo pipefail
D="$(realpath -m "${1:-$(dirname "$0")/../../sp-firmwaremod-upstream/.devroot}")"
A="$(dpkg --print-architecture)"; T="$(gcc -dumpmachine)"
mkdir -p "$D/debs"
( cd "$D/debs" && apt-get download libsdl2-dev libasound2-dev libopus-dev libogg-dev libx11-dev x11proto-dev libxcb1-dev )
for f in "$D"/debs/*.deb; do dpkg -x "$f" "$D"; done
L="$D/usr/lib/$T"
for l in "$L"/*.so; do
  t="$(readlink "$l" || true)"
  [ -n "$t" ] && ln -sf "/usr/lib/$T/$(basename "$t")" "$l"
done
cat > "$D/env.sh" <<ENV
export PKG_CONFIG_SYSROOT_DIR=$D
export PKG_CONFIG_PATH=$L/pkgconfig
export CGO_CFLAGS="-I$D/usr/include -I$D/usr/include/$T -I$D/usr/include/opus -O2"
export CGO_LDFLAGS="-L$L"
export GOTOOLCHAIN=auto
ENV
echo "$D/env.sh"
