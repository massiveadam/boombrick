#!/bin/sh
# plorpOS lists this script on its Ports shelf (Roms/Ports) and runs it to
# start Boombrick, which is in the boombrick folder beside it.
exec sh "$(dirname "$0")/boombrick/launch"
