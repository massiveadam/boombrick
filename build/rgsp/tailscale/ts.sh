#!/bin/sh
# Tailscale on the RG SP, in userspace mode: BaseOS may have no TUN device,
# and this mode needs none, no iptables, and nothing installed. The device
# joins your tailnet as "rgsp": you can reach it with Tailscale SSH, and
# programs on it reach the tailnet through a local proxy (rgsp-sync honours
# HTTP_PROXY; see `ts.sh env`).
#
#   ts.sh start | stop | toggle | status | env
#
# The first start needs an auth key: make one in the Tailscale admin console
# (Settings, Keys) and save it as authkey.txt beside this script. It is used
# once and deleted; the login is then kept in state/. Without a key and not
# logged in, start says so and stops again: there is no browser here.
#
# It runs only while switched on (the Ports shelf's Tailscale toggles it), so
# it costs no battery otherwise.
DIR="$(cd "$(dirname "$0")" && pwd)"
TS_BIN=${TS_BIN:-$DIR}
# The socket and pid file live in /tmp: the card is FAT or exFAT, which
# can't hold a socket, and a socket's path must be short.
RUN=${TS_RUN:-/tmp/rgsp-tailscale}
SOCK=$RUN/tailscaled.sock
PIDF=$RUN/tailscaled.pid
LOG=$DIR/log.txt
SOCKS=${TS_SOCKS:-localhost:1055}
HTTPP=${TS_HTTP:-localhost:1056}
HOSTNAME_TS=${TS_HOSTNAME:-rgsp}

ts() { "$TS_BIN/tailscale" --socket="$SOCK" "$@"; }
say() { echo "$(date '+%F %T') $*" >> "$LOG"; echo "$*"; }

running() { [ -f "$PIDF" ] && kill -0 "$(cat "$PIDF")" 2>/dev/null; }

backend() { # the daemon's state: Running, NeedsLogin, Stopped...
	ts status --json 2>/dev/null | sed -n 's/.*"BackendState": *"\([A-Za-z]*\)".*/\1/p' | head -1
}

start() {
	if running; then say "already running"; return 0; fi
	mkdir -p "$DIR/state" "$RUN"
	# Its own logs stay on the card and aren't uploaded to Tailscale.
	TS_LOGS_DIR="$DIR/state" "$TS_BIN/tailscaled" --tun=userspace-networking --no-logs-no-support \
		--statedir="$DIR/state" --socket="$SOCK" \
		--socks5-server="$SOCKS" --outbound-http-proxy-listen="$HTTPP" >> "$LOG" 2>&1 &
	echo $! > "$PIDF"
	i=0
	while [ $i -lt 50 ] && [ ! -S "$SOCK" ]; do sleep 0.2; i=$((i + 1)); done
	[ -S "$SOCK" ] || { say "tailscaled did not start; see $LOG"; stop; return 1; }
	i=0
	state=$(backend)
	while [ $i -lt 25 ] && { [ -z "$state" ] || [ "$state" = NoState ]; }; do sleep 0.2; i=$((i + 1)); state=$(backend); done
	if [ "$state" = NeedsLogin ] || [ "$state" = Stopped ]; then
		if [ -s "$DIR/authkey.txt" ]; then
			if ts up --ssh --hostname="$HOSTNAME_TS" --accept-dns=false --timeout=60s \
				--auth-key="file:$DIR/authkey.txt" >> "$LOG" 2>&1; then
				rm -f "$DIR/authkey.txt" # used: the login is in state/ now
			else
				say "login with authkey.txt failed; see $LOG"; stop; return 1
			fi
		elif [ "$state" = NeedsLogin ]; then
			say "not logged in: put an auth key in $DIR/authkey.txt and start again"
			stop
			return 1
		else
			ts up --ssh --hostname="$HOSTNAME_TS" --accept-dns=false --timeout=60s >> "$LOG" 2>&1
		fi
	fi
	say "on: $(ts ip -4 2>/dev/null | head -1) as $HOSTNAME_TS"
}

stop() {
	if running; then
		kill "$(cat "$PIDF")" 2>/dev/null
		i=0
		while [ $i -lt 25 ] && running; do sleep 0.2; i=$((i + 1)); done
		running && kill -9 "$(cat "$PIDF")" 2>/dev/null
	fi
	rm -f "$PIDF"
	say "off"
}

case "$1" in
start) start ;;
stop) stop ;;
toggle) if running; then stop; else start; fi ;;
status) if running; then echo "running, $(backend)"; ts ip -4 2>/dev/null; else echo "off"; fi ;;
env) echo "export HTTP_PROXY=http://$HTTPP HTTPS_PROXY=http://$HTTPP ALL_PROXY=socks5://$SOCKS" ;;
*) echo "usage: $0 start|stop|toggle|status|env" >&2; exit 2 ;;
esac
