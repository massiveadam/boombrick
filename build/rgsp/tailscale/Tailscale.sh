#!/bin/sh
# plorpOS lists this on its Ports shelf: each run switches Tailscale on or
# off. What happened is in tailscale/log.txt beside it.
exec sh "$(dirname "$0")/tailscale/ts.sh" toggle
