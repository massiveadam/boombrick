<img src="assets/icon.svg" width="64" height="64" alt="">

# SP FirmwareMod

An offline music player for the Anbernic RG SP: a fork of
[Boombrick](https://github.com/ujjwal313/boombrick) (GPL-3.0) with a
parametric equaliser in the real playback path, the streaming services
switched off, and a one-way library sync from a home server.

Created in [T3 Code](https://t3.codes). Upstream's README is kept as
[README.upstream.md](README.upstream.md); the status of the port, what is
verified and what isn't, is in [docs/PORTING_STATUS.md](docs/PORTING_STATUS.md).

**Status:** runs on a Linux desktop. Not yet run on an RG SP.

## What this fork adds

- **Parametric EQ** (`internal/peq`): up to 20 peak, shelf and pass filters
  with preamp, run on the decoded audio before the sound card, in the
  foreground and background players alike. Equalizer APO / AutoEq
  `ParametricEQ.txt` import, presets, bypass, clipping warnings. Opened from
  the quick menu (Menu → Equalizer).
- **Offline by default** (`internal/features`): no Spotify, YouTube Music,
  downloads, online lookups or upstream updates unless built with the tags
  `streaming`, `netlookups` or `upstreamupdates`.
- **RG SP screen size** with the `rgsp` build tag (720×480).
- **rgsp-sync** (`cmd/rgsp-sync`): one-way, resumable, SHA-256-verified sync
  from a read-only server. See docs/PORTING_STATUS.md, Phase 5.

## Desktop

Headers for SDL2, ALSA, Opus and Ogg are needed: `sudo apt install
libsdl2-dev libasound2-dev libopus-dev libogg-dev`, or without sudo,
`. "$(scripts/devroot.sh)"`.

```
go build -trimpath -tags sqlite_omit_load_extension -o dist/desktop/boombrick ./cmd/boombrick
BOOMBRICK_DATA=dist/desktop/data ./dist/desktop/boombrick -music ~/Music     # your folder
go build ... -tags 'sqlite_omit_load_extension rgsp' ...                        # 720x480 window
```

Keys stand in for the handheld's buttons: arrows = D-pad, K/J/I/U = A/B/X/Y,
Q/E = L1/R1, 1/3 = L2/R2, Return = Start, Tab = Select, Escape = Menu.

Equaliser presets to import go in the `EQ` folder of the music folder.

Testing aids (desktop only):

- `BOOMBRICK_CAPTURE=out.wav` records everything sent to the sound card;
  with `BOOMBRICK_NO_ALSA=1` the file stands in for it (nothing is heard).
- `BOOMBRICK_SCRIPT="3s Menu Down A 1s shot:eq.png quit"` drives the app and
  saves screenshots, e.g. under Xvfb.
- `scripts/desktop-demo.sh` builds, plays a synthetic album with the EQ on
  and bypassed, checks the captures against an independent design
  (`scripts/eq-verify.py`), and writes before/after samples to `dist/demo`.

```
go test -tags sqlite_omit_load_extension $(go list ./... | grep -v -e boombrick-spotify -e spotify/helper)
```

## Licence

GPL-3.0, as Boombrick. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
