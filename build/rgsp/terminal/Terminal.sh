#!/bin/sh
# plorpOS lists this script on its Ports shelf (Roms/Ports) and runs it to
# start the terminal, which is in the terminal folder beside it.
exec sh "$(dirname "$0")/terminal/launch"
