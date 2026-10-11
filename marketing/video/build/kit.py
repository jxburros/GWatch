"""Helpers for the video's scenes: brand, operation builders and motion shorthands.

A ``Scene`` collects Vixl operations (the JSON ``vixl apply`` accepts) and writes one editable
1920 x 1080 ``.vixl`` master with a keyframe timeline. Times are milliseconds. The brand, the
mark and the topology field come from the beta kit (``marketing/beta/build.py``), which takes
them from ``web/app.css``, ``web/logo*.svg`` and ``web/signal-field.svg``.
"""
from __future__ import annotations

import importlib.util
import os
from pathlib import Path

os.environ.setdefault("VIXL_NO_UPDATE", "1")

from vixl import Project  # noqa: E402
from vixl.typefaces import install_font  # noqa: E402

ROOT = Path(__file__).resolve().parents[3]
VIDEO = ROOT / "marketing" / "video"
W, H, FPS = 1920, 1080, 30

_spec = importlib.util.spec_from_file_location("beta_kit", ROOT / "marketing" / "beta" / "build.py")
beta = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(beta)
os.chdir(ROOT)  # the beta kit changes directory on import; screenshot paths here are repository-relative

BG, CARD, ELEV, LINE = beta.BG, beta.CARD, beta.ELEV, beta.LINE
TEXT, MUTED, TEAL, GOLD, NAVY = beta.TEXT, beta.MUTED, beta.TEAL, beta.GOLD, beta.NAVY
UP, WARN, DOWN = beta.UP, beta.WARN, beta.DOWN
GREY = "#5B636C"
HEAD, BODY, SEMI, MONO = beta.HEAD, beta.BODY, beta.SEMI, beta.MONO
VERSION, REPO = beta.VERSION, beta.REPO


class Scene:
    def __init__(self, name: str, duration: int, background: str = BG):
        self.name, self.duration, self.background = name, duration, background
        self.ops: list[dict] = []

    def add(self, *ops):
        for op in ops:
            if isinstance(op, list):
                self.ops.extend(op)
            else:
                self.ops.append(op)
        return self

    # ---------------------------------------------------------------- motion
    def key(self, target, prop, time, value, easing=None):
        op = {"type": "keyframe", "target": target, "property": prop, "time": int(time), "value": value}
        if easing:
            op["easing"] = easing
        self.ops.append(op)

    def keys(self, target, prop, frames, easing="ease-out-cubic"):
        """``frames`` is [(time, value), ...]; every key after the first uses ``easing``."""
        for i, (t, v) in enumerate(frames):
            self.key(target, prop, t, v, easing if i else None)

    def fade(self, target, t_in, t_out=None, dur=350, rise=0, easing="ease-out-cubic"):
        frames = [(0, 0), (t_in, 0), (t_in + dur, 1)]
        if t_out is not None:
            frames += [(t_out, 1), (t_out + dur * 0.8, 0)]
        self.keys(target, "opacity", frames, easing)
        if rise:
            self.keys(target, "translate-y", [(0, rise), (t_in, rise), (t_in + dur * 1.4, 0)], easing)

    def pop(self, target, t, dur=420, start_scale=0.6, t_out=None):
        self.keys(target, "scale", [(0, start_scale), (t, start_scale), (t + dur, 1)], "ease-out-back")
        self.fade(target, t, t_out, dur=int(dur * 0.6))

    def slide(self, target, t, dx=0, dy=0, dur=600, easing="ease-out-expo"):
        if dx:
            self.keys(target, "translate-x", [(0, dx), (t, dx), (t + dur, 0)], easing)
        if dy:
            self.keys(target, "translate-y", [(0, dy), (t, dy), (t + dur, 0)], easing)

    def words(self, target, start, preset="fade-up", unit="word", duration=450, stagger=None, easing="ease-out-cubic"):
        op = {"type": "text-animate", "target": target, "preset": preset, "unit": unit,
              "start": int(start), "duration": int(duration), "easing": easing}
        if stagger is not None:
            op["stagger"] = int(stagger)
        self.ops.append(op)

    # ---------------------------------------------------------------- build
    def project(self) -> Project:
        p = Project(W, H, self.background)
        p._workspace = str(ROOT)  # screenshot paths resolve against the repository root
        install_font(p, "Barlow", 800, role="heading")
        install_font(p, "Barlow", 400, role="body")
        install_font(p, "Barlow", 600)
        install_font(p, "Kode Mono", 500)
        p.apply([{"type": "swatch", "name": "teal", "color": TEAL},
                 {"type": "swatch", "name": "gold", "color": GOLD},
                 {"type": "swatch", "name": "navy", "color": NAVY}], detail="brief")
        batch = [{"type": "timeline-set", "duration": self.duration, "fps": FPS, "loop_mode": "off"}] + self.ops
        for i in range(0, len(batch), 9000):
            p.apply(batch[i:i + 9000], detail="brief")
        return p

    def build(self) -> Project:
        p = self.project()
        p.compact()
        out = VIDEO / "scenes"
        out.mkdir(exist_ok=True)
        p.save(str(out / f"{self.name}.vixl"), overwrite=True)
        return p


# -------------------------------------------------------------------- operation builders
def rect(name, x, y, w, h, fill, radius=0, stroke=None, stroke_width=2):
    op = {"type": "shape", "shape": "rounded-rectangle" if radius else "rectangle", "name": name,
          "x": round(x, 2), "y": round(y, 2), "width": max(1, round(w)), "height": max(1, round(h)), "fill": fill}
    if radius:
        op["radius"] = radius
    if stroke:
        op.update(stroke=stroke, stroke_width=stroke_width)
    return op


def ellipse(name, cx, cy, w, h, fill, stroke=None, stroke_width=2):
    op = {"type": "shape", "shape": "ellipse", "name": name, "x": round(cx - w / 2, 2), "y": round(cy - h / 2, 2),
          "width": max(1, round(w)), "height": max(1, round(h)), "fill": fill}
    if stroke:
        op.update(stroke=stroke, stroke_width=stroke_width)
    return op


def line(name, points, color, width=3, dash=None):
    op = {"type": "pen", "name": name, "points": [[round(x, 2), round(y, 2)] for x, y in points],
          "closed": False, "smooth": False, "stroke": color, "stroke_width": width}
    if dash:
        op["dash"] = dash
    return op


def text(name, value, x, y, size, color=TEXT, font=BODY, **extra):
    return {"type": "text", "name": name, "text": value, "font": font, "size": size, "color": color,
            "x": round(x, 2), "y": round(y, 2), **extra}


def rich(name, spans, x, y, size, width, color=TEXT, font=HEAD, align="left", line_height=1.0):
    """Rich text; ``spans`` is a string or [(text, color or None), ...]."""
    if isinstance(spans, str):
        spans = [(spans, None)]
    out = [{"text": t, **({"color": c} if c else {})} for t, c in spans]
    return {"type": "rich-text", "name": name, "spans": out, "font": font, "size": size, "color": color,
            "x": round(x, 2), "y": round(y, 2), "width": round(width), "align": align, "line_height": line_height}


def centered(name, spans, y, size, color=TEXT, font=HEAD, line_height=1.0, x=0, width=W):
    return rich(name, spans, x, y, size, width, color, font, "center", line_height)


def glow(name, cx, cy, size, color=TEAL, strength="40"):
    return {"type": "gradient", "name": name, "direction": "radial", "width": size, "height": size,
            "x": round(cx - size / 2), "y": round(cy - size / 2),
            "stops": [{"offset": 0, "color": f"{color}{strength}"}, {"offset": 0.6, "color": f"{color}0C"},
                      {"offset": 1, "color": f"{color}00"}]}


def group(name, targets):
    return {"type": "group", "name": name, "targets": list(targets)}


def shadow(target, blur=40, dy=18, opacity=0.5):
    return {"type": "layer-style", "target": target, "name": "drop-shadow",
            "settings": {"color": "#000000", "offset_x": 0, "offset_y": dy, "blur": blur, "opacity": opacity}}


def chip(name, label, x, y, size=26, fg=TEXT, bg=ELEV, font=SEMI, pad_x=22, stroke=None, width=None, dot=None):
    """A pill label. Width is estimated from the font size so scenes need no measuring pass."""
    h = round(size * 1.9)
    lead = round(size * 0.9) if dot else 0
    w = width or round(len(label) * size * (0.62 if font == MONO else 0.52) + 2 * pad_x + lead)
    ops = [rect(f"{name}-bg", x, y, w, h, bg, h / 2, stroke),
           rich(f"{name}-label", label, x + lead, y + (h - size * 1.2) / 2, size, w - lead, fg, font, "center", 1.2)]
    members = [f"{name}-bg", f"{name}-label"]
    if dot:
        d = round(size * 0.5)
        ops.append(ellipse(f"{name}-dot", x + pad_x + d / 2, y + h / 2, d, d, dot))
        members.append(f"{name}-dot")
    return ops + [group(name, members)]


SCREENS = {  # key: (repository-relative file, pixel width, pixel height)
    "dashboard": ("marketing/beta/screens/dashboard-dark.png", 2400, 1461),
    "map": ("marketing/beta/screens/map-dark.png", 2400, 1461),
    "node": ("marketing/beta/screens/node-dark.png", 2400, 1461),
    "wall": ("marketing/beta/screens/wallboard-dark.png", 2400, 1311),
    "discovery": ("marketing/video/screens/discovery-dark.png", 2400, 1461),
    "automation": ("marketing/video/screens/automation-dark.png", 2400, 1461),
}


def window(name, key, x, y, w, h, zoom=1.0, focus=(0, 0), bar=34):
    """A screenshot in an app window. ``zoom`` scales the shot to zoom x the window width and
    ``focus`` is the (fx, fy) fraction of the shot placed at the window's top left, so a window
    can show a crop of the interface. ``{name}-img`` can be keyframed to pan inside the window."""
    path, iw, ih = SCREENS[key]
    sw = w * zoom
    sh = sw * ih / iw
    ix, iy = x - focus[0] * sw, y + bar - focus[1] * sh
    ops = [rect(f"{name}-base", x, y, w, h, "#1B1E24", 14, "#FFFFFF1A", 2)]
    for i, c in enumerate(["#FF5F57", "#FEBC2E", "#28C840"]):
        ops.append(ellipse(f"{name}-tl{i}", x + 26 + i * 22, y + bar / 2, 12, 12, c))
    ops += [rect(f"{name}-win", x, y + bar, w, h - bar, BG),
            {"type": "frame", "name": f"{name}-img", "path": path, "x": round(ix), "y": round(iy),
             "width": round(sw), "height": round(sh), "fit": "fill"},
            {"type": "clip", "target": f"{name}-img", "base": f"{name}-win"},
            shadow(f"{name}-base", 50, 24, 0.55),
            group(name, [f"{name}-base", f"{name}-tl0", f"{name}-tl1", f"{name}-tl2", f"{name}-win",
                         f"{name}-img"])]
    return ops


def backdrop(prefix="bg", glow_at=(1500, 200), strength=0.55):
    """Shared background (the product's topology field and a teal glow), so cuts feel continuous."""
    ops = [rect(f"{prefix}-base", 0, 0, W, H, BG),
           glow(f"{prefix}-glow", *glow_at, 1700, TEAL, "30")]
    ops += beta.field(f"{prefix}-field", W, H, strength=strength)
    return ops


def mark(prefix, x, y, size, variant="dark"):
    """The GWatch magnifier-G with its eye, as editable paths (grouped as ``prefix``)."""
    return beta.mark(prefix, x, y, size, variant)


def kicker(sc, number, label, t=0, x=120, y=70):
    sc.add(chip("kicker", f"{number}  {label}", x, y, size=22, fg=TEAL, bg="#43C9C01F", font=MONO))
    sc.fade("kicker", t, dur=300)


def headline(sc, spans, t=150, y=135, size=76, x=120, width=1680):
    sc.add(rich("headline", spans, x, y, size, width, TEXT, HEAD))
    sc.words("headline", t, "fade-up", "word", 500, 70)
