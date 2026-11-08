Terminal for the RG SP (plorpOS Port)

Unzip Terminal-RGSP.zip at the root of plorpOS's card: it adds
Roms/Ports/Terminal.sh and Roms/Ports/terminal/. Start "Terminal" from the
Ports shelf.

Buttons: D-pad picks a key on the on-screen keyboard, A types it, B is
backspace, Y shows or hides the keyboard, X moves it top or bottom, L1 is
shift while held, R1 latches Ctrl/Alt, Start is Enter, Select is Tab, Menu
quits. A USB or Bluetooth keyboard works too, if the device takes one.

pocket: type `pocket` to open the coding agents' tmux session on your server
over SSH (through Tailscale when its Port has it on). Set POCKET_SSH in
config.sh first.

Push-to-talk: with VOICE_SERVER set in config.sh and the server's token in
voice-token.txt, hold the talk button and speak; the words are typed into
the agent's prompt on the server, not sent. Read them, then press the enter
button. Needs a microphone the device can record from (unverified: the RG SP
lists none; a USB-C one may work).

Unverified on the device: all of it. The program is TermSP built from source
with the alternate screen switched on, the RG SP's buttons and these paths;
see LICENSES.txt.
