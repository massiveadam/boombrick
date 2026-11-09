#!/bin/sh
# Tailscale at boot on the RG SP. plorpOS (patched) runs this from
# System/autostart.d, in the background, so the device is on the tailnet
# whenever it is on, and reachable over SSH from anywhere. The Ports shelf's
# Tailscale still switches it off until the next boot. Delete this file
# (System/autostart.d/tailscale.sh) to have it start only by hand again.
exec sh /mnt/SDCARD/Roms/Ports/tailscale/ts.sh start
