#!/bin/sh
# Boombrick's music sync on the RG SP. plorpOS (patched) runs this at boot
# from System/autostart.d, in the background. Every EVERY seconds it asks
# rgsp-sync to bring Music/Library in step with the server - which it does
# only on a home Wi-Fi network, and on battery only above MIN_BATTERY (a
# charger always qualifies). On battery the device suspends when idle, and
# nothing runs then: it syncs while awake. Each run is resumable and fetches
# only what changed.
#
# Settings: Roms/Ports/boombrick/sync.conf, shell variables:
#   SERVER=http://192.168.1.108:8099     the read-only server (music/ and meta/manifest.json)
#   HOME_SSID=MyWifi                      comma-separated home networks
#   EVERY=1800                            seconds between tries (default 30 min)
#   MIN_BATTERY=30                        least battery, percent, to sync off the charger
#                                         (101: only on the charger)
# Without that file, nothing is done.
SD=/mnt/SDCARD
DIR=$SD/Roms/Ports/boombrick
CONF=$DIR/sync.conf
[ -f "$CONF" ] || exit 0
. "$CONF"
[ -n "$SERVER" ] && [ -n "$HOME_SSID" ] || { echo "boombrick-sync: SERVER and HOME_SSID are needed in $CONF"; exit 0; }
LOG=$DIR/logs/sync.txt
mkdir -p "$DIR/logs" "$SD/Music/Library"
sleep 60 # Wi-Fi first: plorpOS joins in the background
while :; do
	# Kept short: the last run or so is what anyone reads.
	[ -f "$LOG" ] && [ "$(wc -c < "$LOG")" -gt 262144 ] && mv -f "$LOG" "$LOG.1"
	echo "$(date '+%F %T') sync" >> "$LOG"
	"$DIR/bin/rgsp-sync" auto -from "$SERVER/music/" -manifest "$SERVER/meta/manifest.json" \
		-to "$SD/Music/Library" -delete -home-ssid "$HOME_SSID" -min-battery "${MIN_BATTERY:-30}" >> "$LOG" 2>&1
	sleep "${EVERY:-1800}"
done
