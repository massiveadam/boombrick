# RG SP music player — porting status

Last updated 2026-11-07 (second pass). Everything marked **verified** was run on the
development PC (x86-64, Ubuntu 24.04). Nothing has run on an RG SP yet: the
device hasn't arrived. **Mock** marks desktop stand-ins for hardware.

## Where things are

| What | Where |
|---|---|
| This fork (Boombrick + EQ + offline build + RG SP build + sync) | this repository, branch `rgsp` |
| Upstream Boombrick, untouched | branch `main` here (= `upstream/main`, 5915ca2, v0.5.1), and a separate clone in `../sp-firmwaremod-upstream/boombrick` |
| plorpOS reference clone | `../sp-firmwaremod-upstream/plorpOS` (891ffe3) |
| Desktop build headers, user-local (no sudo) | `../sp-firmwaremod-upstream/.devroot` (`scripts/devroot.sh` recreates it) |

Licence: Boombrick is GPL-3.0; the fork stays GPL-3.0 with upstream history,
`LICENSE` and `NOTICE` intact. The Bluetooth fix from upstream PR #5 is
credited to its author (dmaglio) in the code and commit.

### The starter project was not on this machine

The brief describes an `rgsp-music-dev` starter (`internal/peq`,
`cmd/peqwav`, `cmd/peqcurve`, `presets/`, `demo/`, `docs/INTEGRATION.md`,
`scripts/`). It is not in this repository (which held only a README and an
icon) and a search of the filesystem found no `internal/peq` anywhere. So
its tests could not be run and its code could not be reviewed or reused.
The EQ engine was written new at the same path, `internal/peq`. **If you
have the starter elsewhere, point me at it**: I'll diff its DSP against this
one and keep whichever is better, test for test.

## Phase 1 — Boombrick, as it is

All verified unless marked.

- **Build:** Go module `github.com/ujjwal313/boombrick`, `go 1.25`, toolchain
  `go1.27.2` (fetched automatically into the Go cache; the system has 1.22).
  cgo: SDL2 (go-sdl2 v0.4.40), ALSA (`-lasound`), Opus/Ogg (`-lopus -logg`),
  SQLite (mattn/go-sqlite3). FLAC and MP3 decode through bundled dr_flac and
  dr_mp3, AAC through bundled FAAD2, resampling through bundled speexdsp.
- **Desktop:** builds and runs at 1024×768 (verified under Xvfb, and with
  the keyboard mapping in upstream's README). Upstream's tests: 54 packages
  pass; only the Spotify helper doesn't build here (it needs libFLAC and
  mpg123 headers, and is streaming code this fork turns off).
- **Device builds:** `make pak|crossmix|knulli` cross-compile in Docker with
  `ghcr.io/loveretro/tg5040-toolchain` (TrimUI Brick, Allwinner A133P,
  aarch64). Not run here.

### The audio path, and where the EQ goes

```
file ─ decode (dr_flac / dr_mp3 / FAAD2 / opusfile) ─ float32 stereo, source rate
     ─ ReplayGain: audio.WithGain (peak-limited, per source)
     ─ audio.Engine.loop: read 1024 frames
         ─ resample to 48 kHz (speexdsp, quality 4)
         ─ ★ EQ: Processor.Process(out, 48000)          ← inserted here
         ─ sleep-timer fades
         ─ float → int16, clamped
     ─ Output.Write → alsa.Device → snd_pcm_writei("default")
```

`internal/audio/engine.go` is the single place decoded PCM becomes device
audio, for every format and every source (local files and, upstream,
Spotify). The output always runs at 48 kHz, so the EQ is designed once for
48 kHz; the processor still redesigns itself if handed another rate.

- Gapless: a queued source carries on through the same resampler and EQ
  state; the EQ is reset only on a seek or a switch that isn't gapless.
- **Background playback** is a second process (`boombrick -background`) that
  Boombrick starts as it quits; it plays the saved queue through the *same*
  `newPlayer` → `player.New` → `audio.NewEngine` path. So the EQ applies
  there too, as long as that process loads the EQ, which it now does from
  the files the foreground saves (flushed before the hand-over).

## Phase 2 — offline build (done, verified on desktop)

Build tags, all off by default (`internal/features`):

| Tag | Turns on |
|---|---|
| `streaming` | Spotify, YouTube Music, downloads, Spotify import, yt-dlp updates |
| `netlookups` | covers from the Cover Art Archive, lyrics from LRCLIB, font downloads |
| `upstreamupdates` | Settings offering upstream releases (which would *replace this fork*) |

Default build: tab row is **Home · Library**; no Spotify helper process, no
YouTube client, no downloader, no release checks; covers and lyrics come
only from files and folders. The code behind the tags is kept, so the fork
still merges with upstream. `go vet -tags streaming` still passes.

Kept and checked: local playback (FLAC 44.1/48/96 kHz-24-bit, MP3, Opus,
AAC), album/artist/song browsing, folder covers, queue, gapless (50 s album
across three sample rates captured with no gap), ReplayGain path unchanged,
background playback (verified end to end on the desktop, below).

Not yet trimmed: Settings still has the Storage page and Spotify-only rows
hide themselves; nothing visible mentions streaming in the offline build.

## Phase 3 — parametric EQ (done, verified on desktop)

`internal/peq`:

- RBJ-cookbook biquads: **peak, low shelf, high shelf**, plus low/high pass;
  preamp; up to **20 filters**; frequency, gain and Q each adjustable.
- Transposed direct form II, **float64 state per channel**; left and right
  never interact (tested bit-exactly). Denormals flushed in silence.
- Changes from the UI arrive through an atomic hand-off; the preamp ramps
  across a buffer and **bypass crossfades over 20 ms**, so neither clicks.
- **Clipping:** the predicted peak of the response (golden-section refined)
  drives a warning and *Auto preamp*; the processor also counts samples that
  actually leave ±1.0 after the EQ, shown as "Clipping heard".
- **Presets** are plain Equalizer APO text (`Data/eq/presets/*.txt`), saved
  atomically. **Import** reads Equalizer APO / AutoEq `ParametricEQ.txt` from
  the `EQ` folder of the music (on the card) — `PK/PEQ/Modal`, `LS/LSC`,
  `HS/HSC` (centre frequency, as APO defines them), `LP/LPQ/HP/HPQ`, `Q`,
  `BW Oct`, shelf slopes `LSC 12 dB`, multiple `Preamp` lines summed, `OFF`
  filters kept off. Refused with a warning rather than guessed: `BP`, `NO`,
  `AP`, `IIR`, the corner-frequency `LS 6dB/12dB`, `GraphicEQ`, and filters
  limited to one channel by `Channel:`. APO's docs give no default Q for a
  shelf without one; 0.7071 is assumed and the import says so.
- No AirPods preset was made. Two example presets are in `presets/examples`,
  labelled as demonstrations.

**Screen:** quick menu (Menu) → **Equalizer**: response curve with band
markers, On/Off, Preamp (◀▶ 0.5 dB, L1/R1 3 dB), Auto preamp, the bands
(A opens one: ◀▶ picks On/Type/Frequency/Gain/Q, ▲▼ adjusts, L1/R1 in big
steps, X removes), Add a band, Presets (save/load/delete), Import, Reset.
Built from Boombrick's own widgets, fonts and gold Settings focus.

### Verification

| Test | Result |
|---|---|
| `go test ./internal/peq` (+ `-race`): gains at centre, shelf midpoints, Butterworth −3.01 dB, stability over the whole parameter grid at 8–192 kHz, measured sine response = design (±0.02 dB) at 44.1/48/96 kHz, stereo independence, chunking invariance, bypass bit-exact, crossfade, clipping, rate change, preamp ramp, APO parsing and round trip | pass |
| Fuzzing `ParseAPO` for 20 s (1.1 M inputs): never yields an invalid or unstable filter | pass |
| Engine tests: EQ changes the samples written to the output (+6.00 dB at 1 kHz through the resampler), bypassed output bit-identical to no EQ, reset on seek/switch but not gapless | pass |
| **Real playback** captured at the output (`BOOMBRICK_CAPTURE`), EQ on vs bypassed, against an independent SciPy design (`scripts/eq-verify.py`): log sweep 25 Hz–18 kHz | max error **0.045 dB**, RMS 0.017 dB, L=R |
| Same capture, 1 kHz tone (48 kHz FLAC) / 60 Hz tone (96 kHz 24-bit FLAC) | −18.03 vs −17.97 dB; −0.633 vs −0.633 dB |
| **Background player** capture after *Play in background* | same EQ (−18.03 dB / −0.633 dB), no gaps, no clipping |
| CPU, x86 i3-10100T: 10 filters | 70 µs per 1024 frames ≈ **0.33 % of one core** at 48 kHz (20 filters 0.62 %) |

CPU on the H700's Cortex-A53 is **not measured**: no ARM emulator is set
up here (registering one needs a privileged binfmt install). An ARM64 test
binary builds; run `go test -bench . ./internal/peq` on the device.

## Phase 4 — running on the RG SP

The plan is this Boombrick fork running on the RG SP; nothing else. BaseOS
and plorpOS are only the base it runs on: BaseOS boots the device, plorpOS
is the game launcher whose Ports shelf starts Boombrick, and two pieces of
plorpOS's work are reused in the build (its SDL2 display patch and its
record of the RG SP's buttons).

### What the device is (from sources, unverified on hardware)

- The **RG SP** is its own model, not the RG35XX SP or RG34XX SP: Allwinner
  H700 (4× Cortex-A53, Mali-G31), **720×480**, Wi-Fi and Bluetooth (BaseOS
  loads `8821cs` and `rtk_hciattach`: a Realtek RTL8821CS), about 1 GB RAM
  (not confirmed). Sources: BaseOS `devices.json`; plorpOS
  `src/platform_h700.c`, `docs/install-h700.md`.
- **Knulli** doesn't list the RG SP, so Boombrick's existing Knulli build
  isn't a route; nor does ROCKNIX.
- **BaseOS** (pvaibhav/BaseOS): BusyBox on Anbernic's stock kernel, glibc
  **2.35**, **no aarch64 SDL2**, **no KMSDRM**, **no ALSA dmix** (and no SysV
  IPC to build one), BlueZ + **BlueALSA**. It starts the frontend on the
  second card: plorpOS.
- **plorpOS** (MIT) on BaseOS: a Ports shelf for scripts in `Roms/Ports`,
  run through its `pm-run.sh` (the card is `/mnt/SDCARD`); music in
  `/mnt/SDCARD/Music`. With no dmix, plorpOS has its own player and its
  emulators take turns on the sound card; a game is quieted while music
  plays.

### The build (done; not run on the device)

`make rgsp` (`build/rgsp/build.sh`) makes `dist/Boombrick-RGSP.zip`, to
unzip at the root of plorpOS's card:

```
Roms/Ports/Boombrick.sh              listed on the Ports shelf
Roms/Ports/boombrick/launch          sets BOOMBRICK_PLATFORM=rgsp, lib/ first
Roms/Ports/boombrick/boombrick       aarch64, tags rgsp + offline defaults
Roms/Ports/boombrick/lib/            SDL2 2.30.8 (Mali fbdev), libopus, libogg
Roms/Ports/boombrick/bin/rgsp-sync   static
Roms/Ports/boombrick/data, logs      made on first run
```

- **Toolchain:** `build/rgsp/Dockerfile`, plorpOS's recipe: Debian bullseye
  cross-compiler pinned by digest, apt from a dated snapshot, plus arm64 ALSA,
  Opus and Ogg headers. Go is the host's toolchain mounted read-only; Go
  modules come from the host cache with the network off.
- **SDL2:** `build/rgsp/sysroot.sh` builds 2.30.8 with plorpOS's
  `sdl2-h700.patch` (Mali fbdev EGL driver, built-in gamepad as a joystick),
  with plorpOS's options: GLES2, ALSA loaded at run time, no X11/Wayland/DRM.
- **Checked:** the binary and every bundled library need glibc **2.29 at
  most** (BaseOS: 2.35); everything they link resolves to glibc, the bundled
  libraries, or BaseOS's own `libasound.so.2` (kept, so BlueALSA's plugin
  works).
- **Platform profile** `rgsp` (`internal/platform`): data and logs beside
  the app, music in `/mnt/SDCARD/Music` (the same folder plorpOS's player
  reads), Power left to the firmware, and the RG SP's joystick buttons as
  plorpOS logged them on an RG SP (A, B, Y, X, L1, R1, Select, Start, Menu,
  L2, R2 = b0…b10; D-pad hat 0). No self-updates.
- **Background play:** Boombrick's background player starts in its own
  session, so plorpOS ending the port's process group doesn't stop it.

### Done before the device arrives (second round)

- **Sound card shared with games** (`audio.Engine.SetIdleRelease`, on the
  RG SP only): BaseOS has no dmix, so one program holds the codec at a
  time. Boombrick lets go of it after half a second paused or stopped (the
  sleep timer's fade included), as plorpOS's own player does, and waits up
  to a second for a game to let go when it opens it. Engine tests: released
  after the idle time, not during a short pause, released when the queue
  ends, reopened on resume, off by default elsewhere.
- **Output routing** (`internal/audio/route`, `platform.AudioState`), ported
  from plorpOS's `audioout.c`: USB DAC, then wired, then Bluetooth, then
  speaker. Bluetooth is the BlueALSA device plorpOS's `radio.sh` names in
  `/tmp/tortos_btsink`; a DAC is any `USB-Audio` card in
  `/proc/asound/cards`; wired is the PMIC's `spk_state`. When the headset or
  DAC playing goes away, playback pauses instead of carrying on out of the
  speaker; a better output that appears takes over. Unit tests for each.
- **Volume keys, Power and the lid**: plorpOS's launcher keeps reading them
  while a Port runs (its `plat_run` loop), so Boombrick leaves them to it.
  **Backlight and battery**: the RG SP uses the same Allwinner `/dev/disp`
  ioctl and `axp2202-battery` as the Brick, which Boombrick already drives.
- **Four album columns** on the RG SP (190 px cards), three on the Brick as
  before; checked in screenshots at 720×480 and 1024×768. Six upstream tests
  that check the Brick's exact pixels or widths now run on the Brick build
  only; none are lost there, and both builds pass all 58 packages.
- **squiglink import**: its Export writes `<model> Filters.txt` in Equalizer
  APO syntax (LSC/HSC shelves); its own LSQ/HSQ are accepted too, and the
  preset is named after the model. squiglink's own `equalizer.js`, run in
  Node, gives the same response as Boombrick's EQ to 5×10⁻⁷ dB, so AirPods
  Pro 2 filters made on squiglink sound as it graphs them. (Use "Export",
  not "Export Graphic EQ".)
- **Device probe** (`cmd/rgsp-probe`, "Boombrick Probe" on the Ports shelf):
  one run writes `boombrick/logs/probe.txt` with the screen, glibc, sound
  cards, Bluetooth sink, input devices, battery, tools, TUN, storage and the
  EQ's CPU cost on the A53.
- **Tailscale** (optional `dist/Tailscale-RGSP.zip`): Tailscale 1.104.5's
  static arm64 build, checked against its published SHA-256, toggled from
  the Ports shelf. Userspace mode (no TUN or iptables needed), host name
  `rgsp`, Tailscale SSH on, log uploads off, socket in `/tmp` (the FAT card
  can't hold one). First login from `tailscale/authkey.txt`, deleted once
  used. Away from home `rgsp-sync` reaches sanchez through its local proxy
  (`ts.sh env`). Tested on sanchez with the x86 build, never logged in:
  starts, sees it needs a login, says so, stops.

### Cards and the library

- **TF1, 64 GB:** BaseOS (it grows to fill the card; it needs little).
- **TF2, 256 GB:** plorpOS, `Roms/Ports/boombrick`, games and the music.
- The library on sanchez is **346 GB** (12,083 FLAC, 725 M4A of which 135
  ALAC and 10 E-AC-3, 399 MP3, 40 DSF, 20 WAV, 3 Opus; 1,071 album
  folders), measured read-only. It doesn't fit as FLAC, so the player gets a
  copy: `rgsp-sync transcode` encodes the lossless tracks (and E-AC-3, which
  the player can't decode) as **Opus 256 kbps VBR**, and copies AAC, MP3,
  Opus, cover files and lyrics as they are.
  - Encoder: ffmpeg's libopus at 48 kHz through soxr. Not opusenc: it moves
    ReplayGain into the Opus output gain and R128 tags, which Boombrick reads
    as absolute gains on top of the header gain (a track came out 16 dB
    quiet). ffmpeg keeps the REPLAYGAIN tags and an output gain of 0, so a
    track levels as its FLAC does. Sample counts are exact (checked against
    the FLAC, and in `ffmpeg_test.go`), so gapless holds.
  - Embedded pictures are dropped (the folder's cover file is copied); the
    2 albums without one get their first track's picture as `cover.jpg`.
  - Measured on 8 albums: 253 kbit/s on average, about 165× real time with
    6 jobs. Projected total about 97–100 GB.
  - On sanchez: copy in `/srv/media/disk2/rgsp/opus256` (its state in
    `.transcode-state.json` there), log in `/srv/media/disk2/rgsp/transcode.log`.
    The library folder is only read. The copy is on the second disk (ext4,
    USB 3): written to the library's own disk (exFAT on a USB 2 link, while
    the media servers scanned it) it stalled everything else on that disk. Run it again after adding music; it
    redoes only what changed and removes what it made for tracks that are gone.

### Open questions only the device can answer

1. **Does it start?** The Mali driver on fb0, the 720×480 layout, the
   buttons. Does plorpOS take Menu for its own in-game menu while a port
   runs? Boombrick's quick menu is on Menu.
2. **Sound:** speaker and wired through ALSA `default`; AirPods Pro 2 through
   BlueALSA, with the EQ, and the pause when they go back in the case.
3. **Music during games:** plorpOS quiets a game only for its own player
   (`musec_playing()`), so a game started while Boombrick's background
   player plays will find the codec held and run silent; paused, Boombrick
   lets go within half a second. Whether plorpOS's emulator copes with a
   held codec (silent, or refusing to start) is the main thing to check.
   Making plorpOS quiet games for Boombrick would mean changing plorpOS,
   which this fork doesn't.
4. Battery life, suspend with the background player, the EQ's CPU on the
   A53 (the probe measures it), Tailscale on BaseOS.

## Desktop development

```
. "$(scripts/devroot.sh)"        # SDL2/ALSA/Opus headers without sudo
go build -trimpath -tags sqlite_omit_load_extension -o dist/desktop/boombrick ./cmd/boombrick
BOOMBRICK_DATA=dist/desktop/data ./dist/desktop/boombrick -music /path/to/music
```

Add the `rgsp` tag for a 720×480 window. Keys: arrows = D-pad, K/J/I/U =
A/B/X/Y, Q/E = L1/R1, 1/3 = L2/R2, Return = Start, Tab = Select, Escape =
Menu. Test aids: `BOOMBRICK_CAPTURE=out.wav` (with `BOOMBRICK_NO_ALSA=1`,
in place of the sound card), `BOOMBRICK_SCRIPT` (drive the app and save
screenshots), `scripts/desktop-demo.sh` (the capture-and-verify demo).

## Phase 5 — synchronisation (done against temporary folders)

Compared:

| | rsync over SSH | Syncthing | **Manifest over HTTP (chosen)** |
|---|---|---|---|
| Server stays read-only | with `rrsync -ro` | no: two-way by design (send-only folders help, but it runs a full peer) | yes: GET-only server, library mounted `:ro` |
| On the player | needs an aarch64 rsync + ssh | a 20 MB+ daemon, always on, indexing | one static Go binary, runs then exits |
| Battery | walks both trees each run | constant background scanning | one manifest fetch; nothing runs in between |
| Resume | `--partial` | yes | HTTP Range into a partial file |
| Integrity | rolling checksums | block hashes | SHA-256 of every file before it's put in place |

`rgsp-sync` (`cmd/rgsp-sync`, `internal/musicsync`):

- **Server:** `rgsp-sync manifest -root /music -out /meta/manifest.json -cache /meta/hashes.json`
  lists music, covers, lyrics and cue sheets with size, mtime and SHA-256
  (hashes cached by size+mtime, so reruns read only what changed). It
  refuses to write anything inside the library. Serve `/music` and the
  manifest with any read-only HTTP server (e.g. a container on
  sanchez with the library folder mounted `:ro`, as Navidrome
  already mounts it).
- **Player:** `rgsp-sync pull -from http://server:8080/ -to <card>/Music/Library -delete`
  downloads new/changed files into `.rgsp-sync/partial/`, resumes them after
  an interruption, verifies SHA-256, renames into place, writes its state
  after every file, and deletes **only files it synced itself** that the
  server dropped. It checks free space first, refuses unsafe or FAT-invalid
  paths, and refuses a server that ignores Range.
- `rgsp-sync auto -home-ssid … ` adds the gate: home Wi-Fi only (`wpa_cli` or
  `iw`; **unverified which BaseOS has**), and charging or ≥40 % battery
  (sysfs `power_supply`).
- Verified: unit tests (including the server tree byte-for-byte unchanged,
  resume from the exact byte offset, corrupt download discarded, user files
  untouched) and an end-to-end CLI run over HTTP on localhost with copies of
  the synthetic library. **Never run against the real library on sanchez or
  an SD card.**
- Not done: triggering `auto` when Wi-Fi comes up on the device, and a
  running Boombrick noticing a sync (it rescans the library at every start;
  `rgsp-sync` leaves a `.rgsp-sync/last-change` stamp for this).

## Phase 6 — terminal (built; not run on the device)

The handheld as a pocket terminal for coding agents that run on the home
server. Running them on the handheld is ruled out for Claude Code (its docs
ask for 4 GB of RAM; the RG SP has 1 GB) and only an experiment for Codex
(an aarch64 musl build exists; memory use and its sandbox on the vendor
kernel are unknown).

- **Terminal Port:** `build/rgsp/terminal/build.sh` makes
  `dist/Terminal-RGSP.zip`: TermSP (the terminal Terminal.pak wraps for
  NextUI, which lists the RG SP) built from source for plorpOS, with
  libvterm, SDL_fox (no fontconfig: BaseOS lacks it) and a bare FreeType
  compiled in; it needs only SDL2 (shipped), libutil and libc, nothing past
  glibc 2.35. `termsp-rgsp.patch` switches on libvterm's alternate screen
  (upstream never does, so tmux and full-screen TUIs would leave garbage; a
  test against the same libvterm shows the shell's screen restored after a
  full-screen program with it, and not without), maps the RG SP's buttons
  (A types, B backspace, Y keyboard, X moves it, L1 shift, R1 modifiers,
  Start Enter, Select Tab, Menu quits), and sets this Port's paths.
- **`pocket`** opens the server's tmux session over SSH through `tailscale
  nc` (userspace Tailscale routes nothing by itself), with dropbear's
  dbclient shipped in the Port (BaseOS may have no SSH client) and a key
  made on first use (`pocket key` prints it for the server's
  authorized_keys). Checked with a native build of the same dropbear and
  the same script against a throwaway sshd: key login, the proxy hop, and
  the tmux command arriving. The ARM build is linked to libc only and not
  run. `rgsp-probe` reports USB host mode and Bluetooth for a keyboard.
- **Dropped:** voice input. The RG SP lists no microphone; a push-to-talk
  prototype was built and then removed (in git history, commit ef3e3fa and
  after).
- **Later, maybe:** t3code-threads (a CLI for T3 Code threads over its
  WebSocket API) as a structured alternative to typing into a TUI; it
  imports T3's internal contracts, so it may break with T3 updates.

## What remains unverified

- Everything on RG SP hardware: the open questions above, Bluetooth and
  AirPods, suspend, battery life, EQ CPU on the A53.
- AirPods compatibility and battery performance: not claimed.
- The RG SP's exact RAM and radio chip.
- The starter project's code (not found).

## Next concrete step

When the RG SP arrives: BaseOS on the 64 GB card (TF1), plorpOS on the 256 GB
card (TF2) as plorpOS documents; unzip `dist/Boombrick-RGSP.zip` (and, if
wanted, `dist/Tailscale-RGSP.zip`) onto TF2; run **Boombrick Probe** from
Ports and read `Roms/Ports/boombrick/logs/probe.txt`; then start Boombrick
and work through the open questions. First fill of the music, with the card in a reader on sanchez (about
100 GB; far faster than Wi-Fi): `rgsp-sync manifest` on
`/srv/media/disk2/rgsp/opus256`, then `rgsp-sync pull -from <that folder>
-manifest <the manifest> -to <card>/Music/Library`. Pull, not a plain copy:
pull records what it synced on the card, so later pulls over Wi-Fi fetch
only what changed (a plain copy would be fetched again in full).
Before then: export the AirPods Pro 2 filters from squiglink into the card's
`Music/EQ` folder.
