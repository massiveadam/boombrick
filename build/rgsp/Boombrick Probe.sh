#!/bin/sh
# plorpOS lists this on its Ports shelf. It reports what Boombrick needs to
# know about this RG SP into boombrick/logs/probe.txt, and changes nothing.
# Takes a few seconds: the last part times the equaliser.
HERE="$(cd "$(dirname "$0")" && pwd)"
mkdir -p "$HERE/boombrick/logs"
"$HERE/boombrick/bin/rgsp-probe" > "$HERE/boombrick/logs/probe.txt" 2>&1
"$HERE/boombrick/boombrick" -version >> "$HERE/boombrick/logs/probe.txt" 2>&1
