#!/usr/bin/env bash
# Generates a small synthetic music library for desktop tests: tagged FLAC
# (44.1, 48 and 96 kHz / 24-bit), MP3, Opus and AAC, with folder covers and
# two example EQ presets in its EQ folder. No real music is used.
#
#   scripts/make-test-library.sh DIR
set -euo pipefail
M="${1:?usage: make-test-library.sh DIR}"
here="$(cd "$(dirname "$0")/.." && pwd)"
q() { ffmpeg -loglevel error -y "$@"; }
t() { local src=$1 ar=$2 al=$3 ti=$4 tr=$5 out=$6; shift 6
  q -f lavfi -i "$src" -ac 2 "$@" -metadata artist="$ar" -metadata album_artist="$ar" -metadata album="$al" \
    -metadata title="$ti" -metadata track="$tr" -metadata date=2026 "$out"; }
mkdir -p "$M/Test Tones/Sweep Album" "$M/Test Tones/Noise Album" "$M/Synth Band/Chords" "$M/EQ"
q -f lavfi -i "color=c=0xd2553c:s=600x600" -frames:v 1 "$M/Test Tones/Sweep Album/cover.jpg"
q -f lavfi -i "color=c=0x2f6fb0:s=600x600" -frames:v 1 "$M/Test Tones/Noise Album/cover.jpg"
q -f lavfi -i "color=c=0x3c9b5a:s=600x600" -frames:v 1 "$M/Synth Band/Chords/cover.jpg"
A="$M/Test Tones/Sweep Album"
t "aevalsrc='0.3*sin(2*PI*20*(pow(1000\,t/20)-1)*20/log(1000))':s=44100:d=20" "Test Tones" "Sweep Album" "Log Sweep 20Hz-20kHz" 1 "$A/01 Log Sweep.flac" -c:a flac
t "sine=f=1000:sample_rate=48000:d=15" "Test Tones" "Sweep Album" "1 kHz Tone" 2 "$A/02 1kHz.flac" -af volume=0.3 -c:a flac
t "sine=f=60:sample_rate=96000:d=15" "Test Tones" "Sweep Album" "60 Hz Bass 96k" 3 "$A/03 60Hz 96k.flac" -af volume=0.3 -c:a flac -sample_fmt s32
A="$M/Test Tones/Noise Album"
t "anoisesrc=color=pink:sample_rate=44100:amplitude=0.25:seed=3:d=20" "Test Tones" "Noise Album" "Pink Noise MP3" 1 "$A/01 Pink MP3.mp3" -c:a libmp3lame -b:a 192k
t "anoisesrc=color=white:sample_rate=48000:amplitude=0.2:seed=4:d=20" "Test Tones" "Noise Album" "White Noise Opus" 2 "$A/02 White Opus.opus" -c:a libopus -b:a 128k
t "anoisesrc=color=brown:sample_rate=44100:amplitude=0.3:seed=5:d=20" "Test Tones" "Noise Album" "Brown Noise AAC" 3 "$A/03 Brown AAC.m4a" -c:a aac -b:a 192k
A="$M/Synth Band/Chords"; i=1
for f in "220 277 330" "196 247 294" "174 220 262" "165 208 247"; do set -- $f
  t "aevalsrc='0.12*(sin(2*PI*$1*t)+sin(2*PI*$2*t)+sin(2*PI*$3*t))*(0.6+0.4*sin(2*PI*2*t))':s=44100:d=12" \
    "Synth Band" "Chords" "Chord $i" $i "$A/0$i Chord $i.flac" -c:a flac; i=$((i+1)); done
cp "$here"/presets/examples/*.txt "$M/EQ/"
echo "$M"
