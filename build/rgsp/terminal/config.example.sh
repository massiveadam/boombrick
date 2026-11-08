# Settings for the terminal; launch copies this to config.sh the first time.

# Text size: 18 gives about 80 columns by 24 rows at 720x480.
FONT_SIZE=18

# `pocket` (type it in the terminal) opens this tmux session on the server
# over SSH, through Tailscale when it's on. Coding agents run there.
POCKET_SSH=you@your-server
POCKET_SESSION=pocket

# Push-to-talk (rgsp-voice). Off until VOICE_SERVER is set and the server's
# token is saved as voice-token.txt beside this file. Codes: run
# `rgsp-voice keys -input /dev/input/event1` and press the buttons.
VOICE_SERVER=
VOICE_INPUT=/dev/input/event1
VOICE_TALK=0
VOICE_ENTER=0
VOICE_CLEAR=0
VOICE_MIC=default
