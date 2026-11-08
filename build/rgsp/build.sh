#!/bin/sh
# Builds Boombrick for the Anbernic RG SP and packs it as a plorpOS Port:
# dist/Boombrick-RGSP.zip, which unzips at the root of plorpOS's card into
# Roms/Ports/Boombrick.sh and Roms/Ports/boombrick/.
#
#   build/rgsp/build.sh [VERSION]
#
# Needs docker and Go (the toolchain go.mod names is mounted into the build
# container; Go modules come from the host's cache, read-only network off).
# Nothing is installed on the host and nothing touches a device.
set -eu
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT"
VERSION=${1:-dev}
IMAGE=boombrick-rgsp
PORT=dist/rgsp-port/Roms/Ports
APP=$PORT/boombrick

docker build -q -t "$IMAGE" build/rgsp > /dev/null
[ -e dist/rgsp-sysroot/usr/lib/libSDL2-2.0.so.0 ] || build/rgsp/sysroot.sh
go mod download
GOROOT_HOST=$(go env GOROOT)
mkdir -p dist/rgsp dist/rgsp-gocache

docker run --rm --user "$(id -u):$(id -g)" \
	-v "$ROOT:/src" -v "$GOROOT_HOST:/goroot:ro" -v "$(go env GOMODCACHE):/gomod:ro" \
	-v "$ROOT/dist/rgsp-gocache:/gocache" -v "$ROOT/dist/rgsp-sysroot:/sysroot:ro" \
	-e HOME=/tmp -e GOROOT=/goroot -e GOMODCACHE=/gomod -e GOCACHE=/gocache \
	-e GOTOOLCHAIN=local -e GOPROXY=off -e GOFLAGS=-buildvcs=false \
	-e CGO_ENABLED=1 -e GOOS=linux -e GOARCH=arm64 -e CC=aarch64-linux-gnu-gcc \
	-e PKG_CONFIG_LIBDIR=/sysroot/usr/lib/pkgconfig -e PKG_CONFIG_SYSROOT_DIR=/sysroot \
	-e CGO_CFLAGS="-O2 -idirafter /usr/include -I/usr/include/opus" \
	-e CGO_LDFLAGS="-L/usr/lib/aarch64-linux-gnu -L/sysroot/usr/lib" \
	"$IMAGE" sh -euc "
/goroot/bin/go build -trimpath -tags 'sqlite_omit_load_extension rgsp' \
	-ldflags '-s -w -X github.com/ujjwal313/boombrick/internal/version.Version=$VERSION' \
	-o dist/rgsp/boombrick ./cmd/boombrick
CGO_ENABLED=0 /goroot/bin/go build -trimpath -ldflags '-s -w' -o dist/rgsp/rgsp-sync ./cmd/rgsp-sync
CGO_ENABLED=0 /goroot/bin/go build -trimpath -ldflags '-s -w' -o dist/rgsp/rgsp-probe ./cmd/rgsp-probe
CGO_ENABLED=0 /goroot/bin/go build -trimpath -ldflags '-s -w' -o dist/rgsp/rgsp-voice ./cmd/rgsp-voice
# BaseOS is glibc 2.35: nothing may ask for newer.
for f in dist/rgsp/boombrick \$(readlink -f /sysroot/usr/lib/libSDL2-2.0.so.0); do
	if aarch64-linux-gnu-objdump -T \"\$f\" | grep -qE 'GLIBC_2\.(3[6-9]|[4-9][0-9])'; then
		echo \"\$f needs a newer glibc than BaseOS's 2.35\" >&2; exit 1
	fi
done
rm -rf dist/rgsp/lib && mkdir -p dist/rgsp/lib
cp -L /sysroot/usr/lib/libSDL2-2.0.so.0 /usr/lib/aarch64-linux-gnu/libopus.so.0 /usr/lib/aarch64-linux-gnu/libogg.so.0 dist/rgsp/lib/
cp /sysroot/usr/lib/LICENSE.SDL2.txt dist/rgsp/lib/
cp /usr/share/doc/libopus0/copyright dist/rgsp/lib/COPYRIGHT.libopus.txt
cp /usr/share/doc/libogg0/copyright dist/rgsp/lib/COPYRIGHT.libogg.txt
"

rm -rf dist/rgsp-port
mkdir -p "$APP/bin" "$APP/lib"
cp dist/rgsp/boombrick build/rgsp/launch LICENSE NOTICE assets/fonts/OFL-*.txt "$APP/"
cp dist/rgsp/lib/* "$APP/lib/"
cp dist/rgsp/rgsp-sync dist/rgsp/rgsp-probe dist/rgsp/rgsp-voice "$APP/bin/"
cp build/rgsp/sdl2-h700.patch "$APP/lib/"
cp build/rgsp/Boombrick.sh "build/rgsp/Boombrick Probe.sh" "$PORT/"
scripts/licenses.sh dist/rgsp/boombrick > "$APP/THIRD-PARTY.txt"
chmod +x "$APP/boombrick" "$APP/launch" "$APP/bin/rgsp-sync" "$APP/bin/rgsp-probe" "$APP/bin/rgsp-voice" "$PORT/Boombrick.sh" "$PORT/Boombrick Probe.sh"
rm -f dist/Boombrick-RGSP.zip
docker run --rm --user "$(id -u):$(id -g)" -v "$ROOT/dist:/d" -w /d/rgsp-port "$IMAGE" zip -9qr ../Boombrick-RGSP.zip Roms
echo "dist/Boombrick-RGSP.zip"

# Tailscale, optional, in a zip of its own: Roms/Ports/Tailscale.sh and
# Roms/Ports/tailscale/ (build/rgsp/tailscale/README.txt).
TS=dist/rgsp-tailscale/Roms/Ports
rm -rf dist/rgsp-tailscale
mkdir -p "$TS/tailscale"
TSBIN=$(build/rgsp/tailscale/fetch.sh arm64)
cp "$TSBIN/tailscale" "$TSBIN/tailscaled" "$TSBIN/LICENSE" build/rgsp/tailscale/ts.sh build/rgsp/tailscale/README.txt "$TS/tailscale/"
cp build/rgsp/tailscale/Tailscale.sh "$TS/"
chmod +x "$TS/Tailscale.sh" "$TS/tailscale/ts.sh" "$TS/tailscale/tailscale" "$TS/tailscale/tailscaled"
rm -f dist/Tailscale-RGSP.zip
docker run --rm --user "$(id -u):$(id -g)" -v "$ROOT/dist:/d" -w /d/rgsp-tailscale "$IMAGE" zip -9qr ../Tailscale-RGSP.zip Roms
echo "dist/Tailscale-RGSP.zip"
