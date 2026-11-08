#!/usr/bin/env python3
"""Check the equaliser against what Boombrick actually sent to the sound card.

Takes two captures of the same playback made with BOOMBRICK_CAPTURE, one with
the EQ bypassed and one with it on, and the Equalizer APO preset that was
playing. It designs the preset's filters here, independently of the Go code
(RBJ Audio EQ Cookbook, with SciPy), and compares that response with the
ratio of the two captures' spectra.

It also checks the captures for gaps (gapless playback), clipped samples, and
that left and right were processed alike.

    scripts/eq-verify.py OFF.wav ON.wav PRESET.txt [--json out.json]

Needs numpy and scipy.
"""
import argparse
import json
import math
import re
import sys
import wave

import numpy as np
from scipy import signal

RATE = 48000


def read_wav(path):
    with wave.open(path, "rb") as w:
        assert w.getnchannels() == 2 and w.getsampwidth() == 2, "16-bit stereo expected"
        assert w.getframerate() == RATE, w.getframerate()
        data = np.frombuffer(w.readframes(w.getnframes()), dtype="<i2")
    return data.reshape(-1, 2).astype(np.float64) / 32767.0


def parse_apo(path):
    """A deliberately separate, minimal reader for PK/LSC/HSC lines with Q."""
    preamp, filters = 0.0, []
    for line in open(path, encoding="utf-8"):
        line = line.split("#", 1)[0].strip()
        m = re.match(r"(?i)preamp:\s*(-?[\d.]+)\s*db", line)
        if m:
            preamp += float(m.group(1))
            continue
        m = re.match(r"(?i)filter\s*\d*:\s*(ON|OFF)\s+(PK|LSC|HSC)\s+Fc\s+([\d.]+)\s*Hz\s+Gain\s+(-?[\d.]+)\s*dB\s+Q\s+([\d.]+)", line)
        if m and m.group(1).upper() == "ON":
            filters.append((m.group(2).upper(), float(m.group(3)), float(m.group(4)), float(m.group(5))))
    return preamp, filters


def rbj(kind, f0, gain, q, fs=RATE):
    A = 10 ** (gain / 40)
    w0 = 2 * math.pi * f0 / fs
    cw, sw = math.cos(w0), math.sin(w0)
    al = sw / (2 * q)
    if kind == "PK":
        b = [1 + al * A, -2 * cw, 1 - al * A]
        a = [1 + al / A, -2 * cw, 1 - al / A]
    elif kind == "LSC":
        s = 2 * math.sqrt(A) * al
        b = [A * ((A + 1) - (A - 1) * cw + s), 2 * A * ((A - 1) - (A + 1) * cw), A * ((A + 1) - (A - 1) * cw - s)]
        a = [(A + 1) + (A - 1) * cw + s, -2 * ((A - 1) + (A + 1) * cw), (A + 1) + (A - 1) * cw - s]
    else:  # HSC
        s = 2 * math.sqrt(A) * al
        b = [A * ((A + 1) + (A - 1) * cw + s), -2 * A * ((A - 1) + (A + 1) * cw), A * ((A + 1) + (A - 1) * cw - s)]
        a = [(A + 1) - (A - 1) * cw + s, 2 * ((A - 1) - (A + 1) * cw), (A + 1) - (A - 1) * cw - s]
    return np.array(b) / a[0], np.array(a) / a[0]


def theory_db(preamp, filters, freqs):
    h = np.full(len(freqs), 10 ** (preamp / 20), dtype=complex)
    for kind, f0, g, q in filters:
        b, a = rbj(kind, f0, g, q)
        _, hf = signal.freqz(b, a, worN=freqs, fs=RATE)
        h *= hf
    return 20 * np.log10(np.abs(h))


def segments(x, thresh=1e-4, min_gap=0.005):
    """Start and end frames of the stretches of sound, and the silent gaps."""
    loud = np.abs(x).max(axis=1) > thresh
    idx = np.flatnonzero(loud)
    if len(idx) == 0:
        return 0, 0, []
    gaps = []
    d = np.diff(idx)
    for i in np.flatnonzero(d > min_gap * RATE):
        gaps.append((idx[i] / RATE, (idx[i + 1] - idx[i]) / RATE))
    return idx[0], idx[-1], gaps


def band_level(x, lo, hi):
    """RMS of x after a band-pass, in dB."""
    sos = signal.butter(4, [lo, hi], btype="band", fs=RATE, output="sos")
    y = signal.sosfiltfilt(sos, x)
    return 20 * np.log10(np.sqrt(np.mean(y ** 2)) + 1e-20)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("off")
    ap.add_argument("on")
    ap.add_argument("preset")
    ap.add_argument("--json")
    ap.add_argument("--sweep-seconds", type=float, default=20.0)
    args = ap.parse_args()

    off, on = read_wav(args.off), read_wav(args.on)
    preamp, filters = parse_apo(args.preset)
    s_off, e_off, gaps_off = segments(off)
    s_on, e_on, gaps_on = segments(on)
    res = {"preamp_db": preamp, "filters": filters,
           "frames": [len(off), len(on)],
           "gaps_off": gaps_off, "gaps_on": gaps_on,
           "clipped_off": int(np.sum(np.abs(off) >= 32767 / 32767.0)),
           "clipped_on": int(np.sum(np.abs(on) >= 32767 / 32767.0))}

    # Align: each capture's audio starts at its first sound.
    n = min(e_off - s_off, e_on - s_on)
    a, b = off[s_off:s_off + n], on[s_on:s_on + n]

    # The sweep (the first track): response from the spectra's ratio.
    ns = int(args.sweep_seconds * RATE) - RATE // 10
    fa = np.fft.rfft(a[:ns, 0] * np.hanning(ns))
    fb = np.fft.rfft(b[:ns, 0] * np.hanning(ns))
    fbr = np.fft.rfft(b[:ns, 1] * np.hanning(ns))
    freqs = np.fft.rfftfreq(ns, 1 / RATE)
    # 1/6-octave averages of power, 25 Hz to 18 kHz (the sweep's ends are
    # tapered by the window).
    centres = 25 * 2 ** (np.arange(0, 60) / 6)
    centres = centres[centres < 18000]
    meas, meas_r = [], []
    for c in centres:
        sel = (freqs >= c * 2 ** (-1 / 12)) & (freqs < c * 2 ** (1 / 12))
        meas.append(10 * np.log10(np.sum(np.abs(fb[sel]) ** 2) / np.sum(np.abs(fa[sel]) ** 2)))
        meas_r.append(10 * np.log10(np.sum(np.abs(fbr[sel]) ** 2) / np.sum(np.abs(fa[sel]) ** 2)))
    meas, meas_r = np.array(meas), np.array(meas_r)
    # Theory averaged the same way: the response's power over each band.
    th = []
    for c in centres:
        ff = np.geomspace(c * 2 ** (-1 / 12), c * 2 ** (1 / 12), 64)
        th.append(10 * np.log10(np.mean(10 ** (theory_db(preamp, filters, ff) / 10))))
    th = np.array(th)
    err = meas - th
    res["sweep"] = {"centres_hz": centres.round(1).tolist(), "measured_db": meas.round(3).tolist(),
                    "theory_db": th.round(3).tolist(), "error_db": err.round(3).tolist(),
                    "max_abs_error_db": float(np.max(np.abs(err))),
                    "rms_error_db": float(np.sqrt(np.mean(err ** 2))),
                    "left_right_max_diff_db": float(np.max(np.abs(meas - meas_r)))}

    # The 1 kHz tone (second track) and the 60 Hz tone (third): level change.
    t1 = int(args.sweep_seconds * RATE)
    tones = {}
    for name, f0, start, length in [("1 kHz @ 48 kHz", 1000, t1 + RATE, 10 * RATE),
                                    ("60 Hz @ 96 kHz", 60, t1 + 16 * RATE, 10 * RATE)]:
        if start + length > n:
            continue
        lo, hi = f0 / 1.2, f0 * 1.2
        got = band_level(b[start:start + length, 0], lo, hi) - band_level(a[start:start + length, 0], lo, hi)
        want = float(theory_db(preamp, filters, np.array([float(f0)]))[0])
        tones[name] = {"measured_db": round(got, 3), "theory_db": round(want, 3), "error_db": round(got - want, 3)}
    res["tones"] = tones

    if args.json:
        with open(args.json, "w") as f:
            json.dump(res, f, indent=1)
    print(f"preset: preamp {preamp} dB, {len(filters)} filters")
    print(f"captured: {len(off) / RATE:.2f} s bypassed, {len(on) / RATE:.2f} s with the EQ")
    print(f"gaps of silence >5 ms inside the music: bypassed {len(gaps_off)}, EQ {len(gaps_on)}")
    print(f"clipped samples: bypassed {res['clipped_off']}, EQ {res['clipped_on']}")
    sw = res["sweep"]
    print(f"sweep response vs independent theory, {len(centres)} bands 25 Hz-18 kHz: "
          f"max |error| {sw['max_abs_error_db']:.3f} dB, RMS {sw['rms_error_db']:.3f} dB; "
          f"left vs right max diff {sw['left_right_max_diff_db']:.3f} dB")
    for c, m, t in zip(centres[::6], meas[::6], th[::6]):
        print(f"  {c:8.0f} Hz  measured {m:+7.2f} dB  theory {t:+7.2f} dB")
    for k, v in tones.items():
        print(f"tone {k}: measured {v['measured_db']:+.3f} dB, theory {v['theory_db']:+.3f} dB, error {v['error_db']:+.3f} dB")
    ok = sw["max_abs_error_db"] < 0.5 and all(abs(v["error_db"]) < 0.1 for v in tones.values())
    print("PASS" if ok else "FAIL")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
