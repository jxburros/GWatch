"""A numpy synthesiser for the video: a calm 120 BPM score and effects cued to the picture.

Nothing is sampled; every sound is generated here, so the soundtrack rebuilds like the scenes.
The instruments are adapted from Vixl's own promo (promo/build/score.py in the Vixl repository).
``write_all`` writes ``music.wav`` and ``effects.wav`` (48 kHz, 16-bit stereo).
"""
from __future__ import annotations

import wave
from pathlib import Path

import numpy as np

from scenes import hms, scene_at

SR = 48000
BPM = 120
BEAT = 60 / BPM
BAR = 4 * BEAT
rng = np.random.default_rng(11)


def mtof(m):
    return 440.0 * 2 ** ((np.asarray(m, float) - 69) / 12)


def t_axis(seconds):
    return np.arange(int(seconds * SR)) / SR


def spectral(x, lo=None, hi=None, order=2):
    """Zero-phase Butterworth-shaped filter applied in the frequency domain."""
    n = len(x)
    if n == 0:
        return x
    size = 1 << (n - 1).bit_length()
    spec = np.fft.rfft(x, size, axis=0)
    f = np.fft.rfftfreq(size, 1 / SR)
    gain = np.ones_like(f)
    if hi:
        gain /= np.sqrt(1 + (f / hi) ** (2 * order))
    if lo:
        gain /= np.sqrt(1 + (lo / np.maximum(f, 1e-3)) ** (2 * order))
    if spec.ndim == 2:
        gain = gain[:, None]
    return np.fft.irfft(spec * gain, size, axis=0)[:n]


def reverb(x, seconds=1.6, mix=0.25, lo=200, hi=6000):
    n = int(seconds * SR)
    env = np.exp(-np.arange(n) / SR * 5.5 / seconds)
    ir = np.stack([rng.standard_normal(n) * env, rng.standard_normal(n) * env], axis=1)
    ir = spectral(ir, lo, hi)
    ir /= np.sqrt((ir ** 2).sum(axis=0))
    size = 1 << (len(x) + n - 1).bit_length()
    wet = np.fft.irfft(np.fft.rfft(x, size, axis=0) * np.fft.rfft(ir, size, axis=0), size, axis=0)[:len(x)]
    return x * (1 - mix) + wet * mix * 2.2


class Bus:
    def __init__(self, seconds):
        self.buf = np.zeros((int(seconds * SR), 2))

    def add(self, t, x, gain=1.0, pan=0.0):
        """Mix mono or stereo ``x`` at time ``t`` (s); pan -1 left .. 1 right."""
        if x.ndim == 1:
            left, right = np.cos((pan + 1) * np.pi / 4), np.sin((pan + 1) * np.pi / 4)
            x = np.stack([x * left, x * right], axis=1) * np.sqrt(2)
        i = int(round(t * SR))
        if i < 0:
            x, i = x[-i:], 0
        j = min(len(self.buf), i + len(x))
        if j > i:
            self.buf[i:j] += x[:j - i] * gain


# --------------------------------------------------------------------------------- instruments
def kick(punch=1.0):
    t = t_axis(0.45)
    freq = 45 + 110 * np.exp(-t / 0.035)
    body = np.sin(2 * np.pi * np.cumsum(freq) / SR) * np.exp(-t / 0.16)
    click = spectral(rng.standard_normal(len(t)), 1500, 6000) * np.exp(-t / 0.004) * 0.3
    return np.tanh((body + click) * 1.6 * punch) * 0.9


def clap():
    t = t_axis(0.3)
    noise = spectral(rng.standard_normal(len(t)), 900, 5000)
    env = np.zeros_like(t)
    for d in (0, 0.011, 0.022):
        env += (t >= d) * np.exp(-np.maximum(t - d, 0) / (0.012 if d < 0.02 else 0.09))
    return noise * env * 0.5


def hat(open_=False):
    t = t_axis(0.4 if open_ else 0.08)
    x = spectral(rng.standard_normal(len(t)), 7000, 15000)
    return x * np.exp(-t / (0.12 if open_ else 0.018)) * 0.35


def saw(f, t, detune=0.0):
    ph = (f * (1 + detune)) * t
    return 2 * (ph - np.floor(ph + 0.5))


def bass(note, dur):
    t = t_axis(dur)
    f = mtof(note)
    x = 0.7 * saw(f, t) + 0.5 * np.sin(2 * np.pi * f / 2 * t)
    x = spectral(x, 30, 420, 3)
    env = np.minimum(1, t / 0.005) * np.exp(-t / (dur * 0.9))
    env[-int(0.01 * SR):] *= np.linspace(1, 0, int(0.01 * SR))
    return np.tanh(x * env * 1.4) * 0.5


def pad(notes, dur, bright=1200):
    t = t_axis(dur)
    x = sum(saw(mtof(n), t, d) for n in notes for d in (-0.004, 0.0, 0.005))
    x = spectral(x / (3 * len(notes)), 80, bright, 2)
    att, rel = 0.35, 0.5
    env = np.minimum(1, t / att) * np.minimum(1, np.maximum(dur - t, 0) / rel)
    return x * env * 0.9


def pluck(note, dur=0.35, bright=1.0):
    t = t_axis(dur)
    f = mtof(note)
    x = (np.sin(2 * np.pi * f * t) + 0.5 * np.sin(4 * np.pi * f * t) * np.exp(-t / 0.05)
         + 0.25 * bright * np.sin(6 * np.pi * f * t) * np.exp(-t / 0.03))
    return x * np.exp(-t / (dur / 4)) * np.minimum(1, t / 0.002) * 0.35


def bell(note, dur=2.5):
    t = t_axis(dur)
    f = mtof(note)
    x = sum(a * np.sin(2 * np.pi * f * r * t) * np.exp(-t / (dur / k))
            for a, r, k in ((1, 1, 3), (0.5, 2.76, 6), (0.3, 5.4, 10), (0.2, 8.9, 14)))
    return x * np.minimum(1, t / 0.003) * 0.25


def riser(dur):
    t = t_axis(dur)
    u = t / dur
    noise = rng.standard_normal(len(t))
    out = np.zeros_like(t)
    chunks = 24
    for c in range(chunks):  # stepped filter sweep, crossfaded by the envelope's smoothness
        a, b = c * len(t) // chunks, (c + 1) * len(t) // chunks
        out[a:b] = spectral(noise[a:b], 300 + 200 * c, 600 + 500 * c)
    sweep = np.sin(2 * np.pi * np.cumsum(200 + 900 * u ** 2) / SR) * 0.15
    return (out * 0.6 + sweep) * u ** 2


def impact():
    t = t_axis(2.4)
    sub = np.sin(2 * np.pi * np.cumsum(30 + 60 * np.exp(-t / 0.1)) / SR) * np.exp(-t / 0.6)
    crash = spectral(rng.standard_normal(len(t)), 2500, 14000) * np.exp(-t / 0.7) * 0.35
    return np.tanh((sub + crash) * 1.3) * 0.8


def whoosh(dur=0.45):
    t = t_axis(dur)
    u = t / dur
    x = spectral(rng.standard_normal(len(t)), 600, 5000) * np.sin(np.pi * u) ** 2
    return x * 0.25


def tick(note=96):
    t = t_axis(0.08)
    return np.sin(2 * np.pi * mtof(note) * t) * np.exp(-t / 0.012) * 0.3


def pop(note=84):
    t = t_axis(0.18)
    f = mtof(note) * (1 + 0.6 * np.exp(-t / 0.02))
    return np.sin(2 * np.pi * np.cumsum(f) / SR) * np.exp(-t / 0.05) * 0.3


def chime(notes=(84, 88, 91)):
    out = np.zeros(int(0.9 * SR))
    for i, n in enumerate(notes):
        b = bell(n, 0.8)
        k = int(i * 0.06 * SR)
        out[k:k + len(b)] += b[:len(out) - k]
    return out


# --------------------------------------------------------------------------------- score
def alarm():
    """Two falling tones, soft enough to read as a notification rather than a siren."""
    out = np.zeros(int(0.9 * SR))
    for i, n in enumerate((76, 71)):
        t = t_axis(0.38)
        f = mtof(n)
        x = (np.sin(2 * np.pi * f * t) + 0.3 * np.sin(4 * np.pi * f * t)) * np.exp(-t / 0.14)
        x *= np.minimum(1, t / 0.004) * 0.35
        k = int(i * 0.22 * SR)
        out[k:k + len(x)] += x[:len(out) - k]
    return out


# --------------------------------------------------------------------------------- score
# I-V-vi-IV in C, one chord per bar: settled and warm, the "calm" in the tagline.
CHORDS = [(48, [48, 52, 55]), (55, [55, 59, 62]), (57, [57, 60, 64]), (53, [53, 57, 60])]


def music(total, cue):
    """``cue`` maps a scene name to its start in seconds. The arrangement follows the night: a ticking
    cold open, a held breath through the outage, a resolve when the gateway is back, and the groove
    from the title card on."""
    drums, low, keys = Bus(total + 3), Bus(total + 3), Bus(total + 3)
    duck = np.ones(len(drums.buf))
    t_console = cue["02-outage"]
    t_up = cue["02-outage"] + 7.2
    t_title = cue["03-title"]
    t_full = cue["05-tour"]
    t_end = cue["09-end"]
    # Cold open: a clock ticks on the beat over a low A minor drone.
    keys.add(0, pad([57, 60, 64], t_console + 0.6, 800), 0.65)
    low.add(0, pad([33, 45], t_console + 0.4, 260), 0.8)
    for k in range(int(t_console / BEAT)):
        drums.add(k * BEAT, tick(100 if k % 2 == 0 else 95), 0.35, pan=0.2 if k % 2 else -0.2)
    # The outage: a heartbeat and a pulsing low A, alternating A minor and F, until the gateway is back.
    t = t_console
    i = 0
    while t < t_up - 0.01:
        chord = [57, 60, 64] if i % 2 == 0 else [53, 57, 60]
        keys.add(t, pad([n + 12 for n in chord], min(BAR, t_up - t) + 0.4, 650), 0.5)
        for b in range(4):
            bt = t + b * BEAT
            if bt >= t_up:
                break
            if b in (0, 2):
                drums.add(bt, kick(0.55), 0.55)
            for e in range(2):
                et = bt + e * BEAT / 2
                if et < t_up:
                    low.add(et, bass(33, BEAT / 2 * 0.9), 0.55)
        t += BAR
        i += 1
    # Recovery: the first major chord of the film, then arpeggios with no drums.
    keys.add(t_up, bell(84, 2.5), 0.45)
    t = t_up
    bar = 0
    while t < t_title - 0.01:
        root, chord = CHORDS[bar % 4]
        keys.add(t, pad([n + 12 for n in chord], BAR + 0.4, 1300), 0.75)
        for s, idx in enumerate([0, 1, 2, 1, 2, 3, 2, 1]):
            st = t + s * BEAT / 2
            if st < t_title:
                tones = chord + [chord[0] + 12]
                keys.add(st, pluck(tones[idx] + 12, 0.3, 1.0), 0.5, pan=0.35 if s % 2 else -0.35)
        t += BAR
        bar += 1
    keys.add(t_title - 2.5, riser(2.5), 0.25)
    drums.add(t_title, impact(), 0.7)
    # The groove, from the title card to the end card.
    bars = int(np.ceil((t_end - t_title) / BAR))
    for bar in range(bars):
        t0 = t_title + bar * BAR
        root, chord = CHORDS[bar % 4]
        light = t0 < t_full
        keys.add(t0, pad([n + 12 for n in chord], BAR + 0.4, 1300 if light else 1700), 0.72)
        tones = chord + [chord[0] + 12]
        for s, idx in enumerate([0, 1, 2, 1, 2, 3, 2, 1] * 2):
            st = t0 + s * BEAT / 4
            if st < t_end:
                keys.add(st, pluck(tones[idx] + 12, 0.3, 1.0 if light else 1.3), 0.52, pan=0.35 if s % 2 else -0.35)
        for e in range(8):
            et = t0 + e * BEAT / 2
            if et < t_end:
                low.add(et, bass(root - 12 + (12 if e % 4 == 3 else 0), BEAT / 2 * 0.95), 0.85)
        for b in range(4):
            bt = t0 + b * BEAT
            if bt >= t_end:
                continue
            if b in (0, 2) or not light:
                drums.add(bt, kick(0.85 if light else 1.0), 0.9)
                j = int(bt * SR)
                n = int(0.22 * SR)
                duck[j:j + n] = np.minimum(duck[j:j + n], 0.5 + 0.5 * np.linspace(0, 1, n) ** 0.7)
            if b in (1, 3) and not light:
                drums.add(bt, clap(), 0.5, pan=0.05)
            drums.add(bt + BEAT / 2, hat(), 0.45, pan=0.25)
            if not light:
                drums.add(bt + BEAT / 4, hat(), 0.18, pan=-0.25)
                drums.add(bt + 3 * BEAT / 4, hat(), 0.18, pan=-0.25)
    # End card: morning.
    keys.add(t_end - 2.5, riser(2.5), 0.22)
    drums.add(t_end, impact(), 0.6)
    keys.add(t_end, pad([48, 55, 60, 64, 67, 72], 4.0, 1800), 0.95)
    low.add(t_end, bass(36, 2.5), 0.7)
    for i, n in enumerate((72, 76, 79, 84)):
        keys.add(t_end + i * 0.12, bell(n, 3.0), 0.5, pan=-0.4 + i * 0.27)
    keys.buf = reverb(keys.buf, 2.0, 0.32)
    return drums.buf + (low.buf + keys.buf) * duck[:, None]


def effects(total, cue):
    fx = Bus(total + 3)

    def at(scene, ms):
        return cue[scene] + ms / 1000

    for name, start in sorted(cue.items(), key=lambda kv: kv[1])[1:]:
        if name != "03-title":
            fx.add(start - 0.3, whoosh(0.5), 0.6)
    fx.add(at("01-night", 1520), alarm(), 0.9)
    for i in range(8):  # the console assembles
        fx.add(at("02-outage", 250 + i * 60), tick(98 + i % 3), 0.25, pan=-0.4 + 0.1 * i)
    for i in range(11):  # each failing check, as quiet ticks
        t = max(scene_at(hms(23, 42, 3) + i * 3), 900)  # the same clock as shot 2
        fx.add(at("02-outage", t), tick(88), 0.22)
    fx.add(at("02-outage", 1508), pop(84), 0.6)  # the one email
    fx.add(at("02-outage", 7200), chime((84, 88, 91)), 0.7)
    fx.add(at("02-outage", 7600), pop(88), 0.5)
    fx.add(at("03-title", 900), chime((79, 84, 88)), 0.5)
    for i in range(10):
        fx.add(at("04-checks", 300 + i * 160), tick(96 + (i % 4)), 0.3, pan=-0.3 + 0.06 * i)
    for t in (300, 2200, 4300, 6200, 8300, 10200):  # the reticle locks on
        fx.add(at("05-tour", t + 650), tick(102), 0.45)
        fx.add(at("05-tour", t + 700), tick(96), 0.3)
    for i in range(3):
        fx.add(at("06-automate", 600 + i * 1000), pop(74 + 3 * i), 0.45)
        fx.add(at("06-automate", 1400 + i * 1000), tick(100), 0.4)
    for i in range(8):
        fx.add(at("06-automate", 3400 + i * 110), tick(96 + (i % 4)), 0.3, pan=-0.4 + 0.1 * i)
    for i in range(5):
        fx.add(at("07-for", 250 + i * 900), pop(70 + 3 * i), 0.35)
    for i in range(8):
        fx.add(at("08-home", 400 + i * 330), tick(98), 0.28)
    return fx.buf


def write_wav(path, x, peak=0.89):
    x = x / max(1e-9, np.abs(x).max()) * peak
    data = (np.clip(x, -1, 1) * 32767).astype("<i2")
    with wave.open(str(path), "wb") as fh:
        fh.setnchannels(2)
        fh.setsampwidth(2)
        fh.setframerate(SR)
        fh.writeframes(data.tobytes())


def fade(x, total, tail=1.2):
    n = int(total * SR)
    x = x[:n].copy()
    k = int(tail * SR)
    x[-k:] *= np.linspace(1, 0, k)[:, None] ** 1.5
    x[:int(0.02 * SR)] *= np.linspace(0, 1, int(0.02 * SR))[:, None]
    return x


def write_all(directory: Path, cue: dict, total: float):
    """``cue`` maps scene names to start times in milliseconds."""
    seconds = {name: ms / 1000 for name, ms in cue.items()}
    write_wav(directory / "music.wav", fade(music(total, seconds), total), 0.85)
    write_wav(directory / "effects.wav", fade(effects(total, seconds), total), 0.8)
