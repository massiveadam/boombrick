#!/bin/sh
# Builds the SDL2 Boombrick links and ships for the RG SP, into
# dist/rgsp-sysroot. BaseOS has no aarch64 SDL2, so the app carries its own.
#
# SDL 2.30.8 with build/rgsp/sdl2-h700.patch, both zlib. The patch is
# plorpOS's (thwonp/plorpOS, mk/patches/sdl2-h700.patch): a "mali" video
# driver drawing through EGL on /dev/fb0 with the Mali blob (the stock kernel
# has no DRM), and the built-in gamepad recognised as a joystick. The options
# are plorpOS's too (mk/fetch-h700-sysroot.sh): video mali/GLES2, audio ALSA
# loaded at run time, evdev and joystick input, nothing BaseOS lacks.
#
#   build/rgsp/sysroot.sh        (needs docker and the boombrick-rgsp image)
set -eu
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
OUT=$ROOT/dist/rgsp-sysroot
DL=$ROOT/dist/rgsp-dl
SDL_VER=2.30.8
SDL_SHA=380c295ea76b9bd72d90075793971c8bcb232ba0a69a9b14da4ae8f603350058

mkdir -p "$DL"
f=$DL/SDL2-$SDL_VER.tar.gz
[ -f "$f" ] || curl -sSfL -o "$f" "https://github.com/libsdl-org/SDL/releases/download/release-$SDL_VER/SDL2-$SDL_VER.tar.gz"
echo "$SDL_SHA  $f" | sha256sum -c --quiet
rm -rf "$DL/src" "$OUT"
mkdir -p "$DL/src" "$OUT"
tar -xzf "$f" -C "$DL/src"
patch -d "$DL/src/SDL2-$SDL_VER" -p1 -s < "$ROOT/build/rgsp/sdl2-h700.patch"

docker run --rm --user "$(id -u):$(id -g)" -v "$DL/src:/src" -v "$OUT:/out" boombrick-rgsp sh -euc "
cat > /tmp/tc.cmake << T
set(CMAKE_SYSTEM_NAME Linux)
set(CMAKE_SYSTEM_PROCESSOR aarch64)
set(CMAKE_C_COMPILER aarch64-linux-gnu-gcc)
set(CMAKE_CXX_COMPILER aarch64-linux-gnu-g++)
T
cmake -S /src/SDL2-$SDL_VER -B /tmp/b -DCMAKE_TOOLCHAIN_FILE=/tmp/tc.cmake -DCMAKE_BUILD_TYPE=Release \
	-DCMAKE_INSTALL_PREFIX=/usr -DCMAKE_C_FLAGS=-I/src/SDL2-$SDL_VER/src/video/khronos \
	-DSDL_STATIC=OFF -DSDL_TEST=OFF -DSDL_RPATH=OFF \
	-DSDL_MALI=ON -DSDL_OPENGLES=ON -DSDL_OPENGL=OFF -DSDL_X11=OFF -DSDL_WAYLAND=OFF \
	-DSDL_KMSDRM=OFF -DSDL_VIVANTE=OFF -DSDL_VULKAN=OFF -DSDL_DIRECTFB=OFF -DSDL_RPI=OFF \
	-DSDL_OFFSCREEN=OFF -DSDL_ALSA=ON -DSDL_ALSA_SHARED=ON -DSDL_PULSEAUDIO=OFF \
	-DSDL_PIPEWIRE=OFF -DSDL_JACK=OFF -DSDL_SNDIO=OFF -DSDL_OSS=OFF -DSDL_LIBSAMPLERATE=OFF \
	-DSDL_DBUS=OFF -DSDL_IBUS=OFF -DSDL_LIBUDEV=OFF -DSDL_HIDAPI=OFF > /tmp/sdl.log 2>&1 &&
cmake --build /tmp/b -j\$(nproc) >> /tmp/sdl.log 2>&1 &&
DESTDIR=/out cmake --install /tmp/b >> /tmp/sdl.log 2>&1 || { tail -30 /tmp/sdl.log; exit 1; }
grep -q 'define SDL_VIDEO_DRIVER_MALI 1' /out/usr/include/SDL2/SDL_config.h || { echo 'SDL2 built without the mali driver' >&2; exit 1; }
aarch64-linux-gnu-strip --strip-unneeded \$(readlink -f /out/usr/lib/libSDL2-2.0.so.0)
"
cp "$DL/src/SDL2-$SDL_VER/LICENSE.txt" "$OUT/usr/lib/LICENSE.SDL2.txt"
echo "sysroot: $OUT"
