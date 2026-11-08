Tailscale for the RG SP (optional)

Tailscale.sh on the Ports shelf switches Tailscale on or off. It runs in
userspace mode, as "rgsp" on your tailnet, with Tailscale SSH on, and only
while switched on.

First time: in the Tailscale admin console make an auth key (Settings,
Keys; one-off, pre-approved), save it as tailscale/authkey.txt on the card,
then run Tailscale from Ports. The key is deleted once used; the login is
kept in tailscale/state. tailscale/log.txt says what happened.

Away from home, rgsp-sync reaches sanchez through Tailscale's local proxy:
  eval "$(sh /mnt/SDCARD/Roms/Ports/tailscale/ts.sh env)"
  rgsp-sync pull -from http://<server Tailscale IP>:<port>/ -to /mnt/SDCARD/Music ...

Tailscale is BSD-3-Clause (LICENSE). Not yet tried on the RG SP.
