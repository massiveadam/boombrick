# RG SP music player — porting status

Last updated 2026-11-07. Everything marked **verified** was run on the
development PC (x86-64, Ubuntu 24.04). Nothing has run on an RG SP yet: the
device hasn't arrived. **Mock** marks desktop stand-ins for hardware.

## Where things are

| What | Where |
|---|---|
| This fork (Boombrick + EQ + offline build + sync) | this repository, branch `rgsp` |
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

### What the device is (from sources, unverified on hardware)

- The **RG SP** is its own model, distinct from the RG35XX SP and RG34XX SP:
  Allwinner H700 (4× Cortex-A53, Mali-G31), **720×480** panel, Wi-Fi +
  Bluetooth (BaseOS's H700 path loads `8821cs` and `rtk_hciattach`, i.e. a
  Realtek RTL8821CS), about 1 GB RAM (not confirmed). Sources: BaseOS
  `devices.json`; plorpOS `platform_h700.c` and `docs/install-h700.md`.
- **Knulli does not list the RG SP** (it lists the RG35XX SP and RG34XX). Its
  background music is EmulationStation's, not over games. ROCKNIX lists the
  RG34XX SP and RG35XX SP, not the RG SP.
- **BaseOS** (pvaibhav/BaseOS): a BusyBox userland on Anbernic's stock kernel,
  glibc **2.35**; no menu of its own — it starts the frontend found on the
  second card. **No aarch64 SDL2** (only stock armhf), **no KMSDRM** in the
  stock kernel, **no ALSA dmix and no SysV IPC** to build one.
- **plorpOS** (MIT) runs on BaseOS on the RG SP. It builds SDL 2.30.8 with a
  Mali **fbdev** video driver and GLES2 renderer, dlopens ALSA, uses BlueZ +
  **BlueALSA** for Bluetooth, and its music player **Muse** is a separate C
  process (FFmpeg decode) driven over a line protocol. Because there's no
  dmix, Muse and the emulator **take turns on the codec**: while music
  plays during a game, the game is quieted. Output priority: USB DAC, wired,
  Bluetooth, speaker.

### The two approaches compared

| | A. Boombrick fork on BaseOS, launched from plorpOS | B. Extend plorpOS's Muse to look like Boombrick |
|---|---|---|
| Design fidelity | Boombrick's real UI (shown working at 720×480) | Re-drawn in plorpOS's C UI: approximate |
| EQ | Done (Go), verified | Port `peq` to C (~250 lines) and its tests; presets are the same APO files |
| Bluetooth | ALSA `default` + BlueALSA; upstream PR #5's thread fix carried over (it was for AirPods on bluealsa). Sink selection to write | Solved by plorpOS |
| Music during games | Boombrick's background process exists, but must take part in plorpOS's codec hand-over with its emulator: plorpOS-internal, unknown effort | Solved by plorpOS (game quieted) |
| Hardware code to write | A `baseos` platform profile: paths, volume (codec `lineout volume`), backlight, lid/suspend, battery, BT sink; bundle plorpOS's SDL2 | Little |
| Lid, sleep, power | To write | Solved |
| Language | Go (this codebase) | C |

### Recommendation

**A, with B's EQ as the safety net, decided by three early on-device
tests.** A keeps the design you chose, and its hardware code is confined to
one `internal/platform` profile plus bundled libraries; the EQ, offline
build, sync and UI are done. B is cheaper in hardware terms but means
rebuilding the UI in C.

The deciding risk is music during games. Order of work once the RG SP
arrives:

1. Install BaseOS + plorpOS as documented by plorpOS (no firmware of ours).
   Confirm the panel size, RAM, glibc and which of `wpa_cli`/`iw`/`amixer`
   exist.
2. **Test 1 — display/input:** run the `rgsp` build against plorpOS's
   SDL2 (fbdev/Mali) as a Port; check the controller mapping.
3. **Test 2 — audio:** wired, speaker, then AirPods through BlueALSA, with
   the EQ on; measure CPU with `perf` mode and `go test -bench`.
4. **Test 3 — background with a game:** hand the music to the background
   player and start a game from plorpOS. If Boombrick can't share the codec
   with plorpOS's emulator without deep plorpOS changes, keep Boombrick for
   listening and port the EQ into Muse (B) for in-game music, reading the
   same preset files.

### Toolchain and packaging (not yet built)

- **Cross-compiler:** none installed here (no `aarch64-linux-gnu-gcc`).
  Options: upstream's Docker image (glibc of TrimUI's SDK; a binary built
  against an older glibc runs on 2.35 — to check with `objdump -T`), or
  plorpOS's `mk/fetch-h700-sysroot.sh` sysroot (glibc 2.35, SDL 2.30.8
  fbdev). The latter matches the target; **use it**.
- **Libraries to bundle** in the app's `lib/`: SDL2 (plorpOS's build),
  libopus, libogg; check that BaseOS has libasound and libsqlite3 or bundle
  them. Keep every glibc symbol ≤ 2.35 (`objdump -T`).
- **Graphics:** SDL's Mali fbdev driver, GLES2 renderer. The `rgsp` tag sets
  720×480; untested on the real driver.
- **Input:** SDL joystick/game controller. Boombrick already reads game
  controllers (its Knulli mode); the RG SP's mapping is to write and check.
- **Audio routing:** ALSA `default` (codec, no dmix) or the BlueALSA PCM for
  Bluetooth, following plorpOS's priority. To write.
- **Install format:** a folder under plorpOS's `Roms/Ports` with a launch
  script, as Boombrick's Knulli build does (`build/knulli/`). Not flashed,
  not partitioned: nothing here touches firmware. Unverified that plorpOS
  starts native ports outside PortMaster.

`rgsp-sync` is pure Go and builds as a **static** aarch64 binary
(`CGO_ENABLED=0 GOARCH=arm64`), so it needs nothing from BaseOS.

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
  manifest with any read-only HTTP server (e.g. an nginx container on
  Unraid with the share mounted `:ro`).
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
  the synthetic library. **Never run against your Unraid library or an SD
  card.**
- Not done: triggering `auto` when Wi-Fi comes up on the device, and a
  running Boombrick noticing a sync (it rescans the library at every start;
  `rgsp-sync` leaves a `.rgsp-sync/last-change` stamp for this).

## What remains unverified

- Anything on RG SP hardware: display driver, input mapping, audio devices,
  BlueALSA/AirPods, the codec hand-over with games, suspend/lid, battery
  life, CPU load of the EQ on the A53.
- AirPods compatibility and battery performance: not claimed.
- That the `rgsp` binary links against plorpOS's sysroot (not built).
- The RG SP's exact RAM and radio chip.
- The starter project's code (not found).

## Next concrete step

Build the `rgsp` binary against plorpOS's H700 sysroot
(`mk/fetch-h700-sysroot.sh`, glibc 2.35, SDL 2.30.8 fbdev) in Docker,
check its glibc symbols with `objdump -T`, and package it as a plorpOS Port
folder — so it's ready for Test 1 the day the RG SP arrives.
