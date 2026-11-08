#!/bin/sh
# Builds a terminal for the Anbernic RG SP as a plorpOS Port:
# dist/Terminal-RGSP.zip, which unzips at the root of plorpOS's card into
# Roms/Ports/Terminal.sh and Roms/Ports/terminal/.
#
#   build/rgsp/terminal/build.sh
#
# The terminal is TermSP (Nevrdid/TermSP, MIT): SDL2, libvterm and a real PTY,
# with an on-screen keyboard driven by the D-pad. Terminal.pak
# (josegonzalez/minui-terminal-pak, MIT) packs it for NextUI and lists the RG
# SP; this builds the same program for plorpOS from source, with
# termsp-rgsp.patch: the alternate screen switched on (tmux and full-screen
# programs need it), the RG SP's button order, and this Port's paths.
#
# Everything but SDL2 and the C library is compiled in: libvterm (in
# TermSP's tree), SDL_fox (palomena/SDL_fox, MIT) without fontconfig, which
# BaseOS lacks, and a FreeType with no optional dependencies. SDL2 is the one
# Boombrick ships (build/rgsp/sysroot.sh). Needs docker and the
# boombrick-rgsp image; nothing touches a device.
set -eu
ROOT=$(cd "$(dirname "$0")/../../.." && pwd)
cd "$ROOT"
HERE=build/rgsp/terminal
DL=dist/rgsp-dl
OUT=dist/rgsp-terminal
PORT=$OUT/Roms/Ports
APP=$PORT/terminal

TERMSP=5116aeda84b8d4bb125a214464c131c177260140
TERMSP_SHA=ba3ebdbb8c2dd2e5e04f8125f7fc88a4fa2318d6bab69ab827b78786ecad1fcb
FOX=8eb043dafad3255932f6289fab7996842e7a2203
FOX_SHA=27867318868170019041f15ed3e28584d42f7a63eeb70ad0a2ce538006a789a2
FT=2.13.3
FT_SHA=0550350666d427c74daeb85d5ac7bb353acba5f76956395995311a9c6f063289
HACK=v3.003
HACK_SHA=7f592d091cbd2472f39af96e6c280cde3c27cab50a9c88746feb42918aa1888f

fetch() { # file url sha
	[ -f "$DL/$1" ] || curl -sSfL -o "$DL/$1" "$2"
	echo "$3  $DL/$1" | sha256sum -c --quiet
}
mkdir -p "$DL"
fetch "termsp-${TERMSP%"${TERMSP#???????}"}.tar.gz" "https://github.com/Nevrdid/TermSP/archive/$TERMSP.tar.gz" "$TERMSP_SHA"
fetch "sdlfox-$FOX.tar.gz" "https://github.com/palomena/SDL_fox/archive/$FOX.tar.gz" "$FOX_SHA"
fetch "freetype-$FT.tar.xz" "https://download.savannah.gnu.org/releases/freetype/freetype-$FT.tar.xz" "$FT_SHA"
fetch "Hack-$HACK-ttf.tar.gz" "https://github.com/source-foundry/Hack/releases/download/$HACK/Hack-$HACK-ttf.tar.gz" "$HACK_SHA"

docker build -q -t boombrick-rgsp build/rgsp > /dev/null
[ -e dist/rgsp-sysroot/usr/lib/libSDL2-2.0.so.0 ] || build/rgsp/sysroot.sh

SRC=dist/rgsp-terminal-src
rm -rf "$SRC" "$OUT"
mkdir -p "$SRC" "$APP/fonts" "$APP/lib"
tar -xzf "$DL/termsp-5116aed.tar.gz" -C "$SRC"
mv "$SRC/TermSP-$TERMSP" "$SRC/termsp"
tar -xzf "$DL/sdlfox-$FOX.tar.gz" -C "$SRC"
mv "$SRC/SDL_fox-$FOX" "$SRC/sdlfox"
tar -xJf "$DL/freetype-$FT.tar.xz" -C "$SRC"
mv "$SRC/freetype-$FT" "$SRC/freetype"
patch -d "$SRC/termsp" -p1 -s < "$HERE/termsp-rgsp.patch"
# TermSP's tree ships prebuilt libraries; only sources are used here.
rm -rf "$SRC/termsp/libs" "$SRC/termsp/libvterm-0.3.3"/src/*.o "$SRC/termsp/libvterm-0.3.3"/src/*.lo

docker run --rm --user "$(id -u):$(id -g)" -v "$ROOT/$SRC:/src" -v "$ROOT/dist/rgsp-sysroot:/sysroot:ro" boombrick-rgsp sh -euc '
cat > /tmp/tc.cmake << T
set(CMAKE_SYSTEM_NAME Linux)
set(CMAKE_SYSTEM_PROCESSOR aarch64)
set(CMAKE_C_COMPILER aarch64-linux-gnu-gcc)
T
cmake -S /src/freetype -B /tmp/ft -DCMAKE_TOOLCHAIN_FILE=/tmp/tc.cmake -DCMAKE_BUILD_TYPE=Release \
	-DCMAKE_POSITION_INDEPENDENT_CODE=ON -DBUILD_SHARED_LIBS=OFF \
	-DFT_DISABLE_ZLIB=ON -DFT_DISABLE_BZIP2=ON -DFT_DISABLE_PNG=ON \
	-DFT_DISABLE_HARFBUZZ=ON -DFT_DISABLE_BROTLI=ON > /tmp/ft.log 2>&1 &&
cmake --build /tmp/ft -j$(nproc) >> /tmp/ft.log 2>&1 || { tail -30 /tmp/ft.log; exit 1; }
VT=/src/termsp/libvterm-0.3.3
aarch64-linux-gnu-gcc -O2 -flto -Wall -Wno-unused-result \
	-I/sysroot/usr/include -I/sysroot/usr/include/SDL2 -I/src/termsp/includes \
	-I$VT/include -I/src/freetype/include \
	-DSCREENRC=\"/mnt/SDCARD/Roms/Ports/terminal/screenrc\" \
	-DTERMSP_LIB=\"/mnt/SDCARD/Roms/Ports/terminal/lib\" \
	-DTERMSP_FONTS=\"/mnt/SDCARD/Roms/Ports/terminal/fonts\" \
	/src/termsp/src/*.c /src/sdlfox/src/SDL_fox.c $VT/src/*.c \
	-o /src/termsp-rgsp -Wl,--gc-sections \
	-L/sysroot/usr/lib -lSDL2 /tmp/ft/libfreetype.a -lutil -lm
aarch64-linux-gnu-strip /src/termsp-rgsp
if aarch64-linux-gnu-objdump -T /src/termsp-rgsp | grep -qE "GLIBC_2\.(3[6-9]|[4-9][0-9])"; then
	echo "termsp needs a newer glibc than BaseOS has" >&2; exit 1
fi
aarch64-linux-gnu-objdump -p /src/termsp-rgsp | sed -n "s/ *NEEDED *//p" | tr "\n" " "; echo
'

cp "$SRC/termsp-rgsp" "$APP/termsp"
tar -xzf "$DL/Hack-$HACK-ttf.tar.gz" -C "$APP/fonts" --strip-components=1 ttf/Hack-Regular.ttf ttf/Hack-Bold.ttf
cp -L dist/rgsp-sysroot/usr/lib/libSDL2-2.0.so.0 dist/rgsp-sysroot/usr/lib/LICENSE.SDL2.txt "$APP/lib/"
cp "$HERE/launch" "$HERE/pocket" "$HERE/README.txt" "$HERE/config.example.sh" "$APP/"
cp "$HERE/Terminal.sh" "$PORT/"
{
	echo "TermSP (https://github.com/Nevrdid/TermSP), MIT:"; echo; cat "$SRC/termsp/LICENCE"; echo
	echo "libvterm 0.3.3 (in TermSP's tree), MIT:"; echo; cat "$SRC/termsp/libvterm-0.3.3/LICENSE"; echo
	echo "SDL_fox (https://github.com/palomena/SDL_fox), MIT:"; echo; cat "$SRC/sdlfox/LICENSE"; echo
	echo "FreeType $FT, under the FreeType License (FTL):"; echo; cat "$SRC/freetype/docs/FTL.TXT"; echo
	echo "Hack $HACK fonts: MIT, with the Bitstream Vera licence for the Vera glyphs; see https://github.com/source-foundry/Hack/blob/master/LICENSE.md"
	echo; echo "The input mapping follows Terminal.pak (https://github.com/josegonzalez/minui-terminal-pak), MIT."
} > "$APP/LICENSES.txt"
chmod +x "$APP/termsp" "$APP/launch" "$APP/pocket" "$PORT/Terminal.sh"
rm -f dist/Terminal-RGSP.zip
docker run --rm --user "$(id -u):$(id -g)" -v "$ROOT/dist:/dist" boombrick-rgsp sh -c 'cd /dist/rgsp-terminal && zip -qr /dist/Terminal-RGSP.zip Roms'
echo "dist/Terminal-RGSP.zip"
