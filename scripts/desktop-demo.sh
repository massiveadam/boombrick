#!/usr/bin/env bash
# The desktop demo: builds the offline player, plays the synthetic Sweep
# Album twice under Xvfb, once with the Demo Smile EQ bypassed and once with
# it on, capturing what is sent to the sound card (the capture stands in for
# it: nothing is heard), then checks the EQ in the captures against an
# independent design and cuts before/after samples.
#
#   scripts/desktop-demo.sh            (output in dist/demo)
#
# Needs Go, ffmpeg, Xvfb (unless $DISPLAY is set), python3 with numpy and
# scipy, and the SDL2/ALSA/Opus headers (system, or scripts/devroot.sh).
set -euo pipefail
cd "$(dirname "$0")/.."
OUT="$PWD/dist/demo"; rm -rf "$OUT"; mkdir -p "$OUT"
if ! pkg-config --exists sdl2 2>/dev/null; then
  ENV="${DEVROOT_ENV:-$PWD/../sp-firmwaremod-upstream/.devroot/env.sh}"
  [ -f "$ENV" ] || ENV="$(scripts/devroot.sh)"
  . "$ENV"
fi
go build -trimpath -tags sqlite_omit_load_extension -o dist/desktop/boombrick ./cmd/boombrick
scripts/make-test-library.sh "$OUT/testlib" >/dev/null
if [ -z "${DISPLAY:-}" ]; then
  Xvfb :79 -screen 0 1100x800x24 >/dev/null 2>&1 & XPID=$!; trap 'kill $XPID' EXIT; export DISPLAY=:79; sleep 1
fi
run() { # mode bypass
  local R="$OUT/$1"; mkdir -p "$R/data/eq" "$R/shots"
  cp "presets/examples/Demo Smile.txt" "$R/data/eq/current.txt"
  echo "{\"bypass\":$2,\"preset\":\"Demo Smile\"}" > "$R/data/eq/state.json"
  BOOMBRICK_DATA="$R/data" BOOMBRICK_MUSIC="$OUT/testlib" BOOMBRICK_CAPTURE="$R/capture.wav" BOOMBRICK_NO_ALSA=1 \
  BOOMBRICK_SCRIPT="3s R1 600ms Right Right A 800ms A 3s Start 1500ms shot:$R/shots/now-playing.png B Menu 700ms Down A 900ms shot:$R/shots/equalizer.png B 50s quit" \
    timeout 120 ./dist/desktop/boombrick > "$R/log.txt" 2>&1
}
run bypassed true & P1=$!; run eq false & P2=$!; wait $P1 $P2   # not Xvfb
python3 scripts/eq-verify.py "$OUT/bypassed/capture.wav" "$OUT/eq/capture.wav" "presets/examples/Demo Smile.txt" --json "$OUT/verify.json" | tee "$OUT/verify.txt"
for m in bypassed eq; do
  ffmpeg -loglevel error -y -i "$OUT/$m/capture.wav" -t 20 -c:a flac "$OUT/sweep-$m.flac"
done
echo "demo in $OUT: verify.txt, sweep-bypassed.flac, sweep-eq.flac, */shots/*.png"
