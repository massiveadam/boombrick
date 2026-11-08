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

Unverified on the device: all of it. The program is TermSP built from source
with the alternate screen switched on, the RG SP's buttons and these paths;
see LICENSES.txt.
