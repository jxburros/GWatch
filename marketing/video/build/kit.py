"""Helpers for the video's scenes: the interface's design language as Vixl operations.

GWatch's interface ("Signal", web/app.css) is a calm control centre on warm graphite: square corners,
hairline panels with tracked Kode Mono labels, Barlow for words and Kode Mono for every number, and
soft glows that mean status and only status. The helpers here draw those pieces, so the video looks
like the product rather than like a slide deck.

A ``Scene`` collects Vixl operations (the JSON ``vixl apply`` accepts) and writes one editable
1920 x 1080 ``.vixl`` master with a keyframe timeline. Times are milliseconds. The mark comes from
the logo kit in brand/, through the beta kit's ``mark()``.
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

# Tokens from web/app.css (dark theme).
BG, CARD, ELEV, ELEV2 = "#0F1114", "#16181D", "#22262D", "#2A2F37"
LINE, LINE_STRONG = "#262A31", "#333941"
TEXT, MUTED, DIM, ZERO = "#F2F4F6", "#9EA7AF", "#929BA5", "#67717B"
TEAL, UP, DOWN, WARN, UNKNOWN, MAINT = "#43C9C0", "#35E07F", "#FF5C5C", "#FFC542", "#8B949E", "#6EA0FF"
A_GIT, A_SCRIPT = "#FF8A65", "#3EC8B8"
SCRIM = "#030406B8"
GOLD, NAVY = beta.GOLD, beta.NAVY
HEAD, SEMI, BODY, MONO = "barlow-700", "barlow-600", "barlow-400", "kode-mono-500"
VERSION, REPO = beta.VERSION, beta.REPO


def soft(color, alpha="24"):
    """A status colour's wash (the --*-soft tokens are the colour at about 14%)."""
    return f"{color}{alpha}"


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

    def fade(self, target, t_in, t_out=None, dur=300, rise=0, easing="ease-out-cubic"):
        frames = [(0, 0), (t_in, 0), (t_in + dur, 1)]
        if t_out is not None:
            frames += [(t_out, 1), (t_out + dur, 0)]
        self.keys(target, "opacity", frames, easing)
        if rise:
            self.keys(target, "translate-y", [(0, rise), (t_in, rise), (t_in + dur * 1.5, 0)], easing)

    def slide(self, target, t, dx=0, dy=0, dur=500, easing="ease-out-expo"):
        if dx:
            self.keys(target, "translate-x", [(0, dx), (t, dx), (t + dur, 0)], easing)
        if dy:
            self.keys(target, "translate-y", [(0, dy), (t, dy), (t + dur, 0)], easing)

    def texts(self, target, frames):
        """Swap a text layer's content at each (time, value)."""
        for t, v in frames:
            self.key(target, "text", t, v)

    def words(self, target, start, preset="fade-up", unit="word", duration=420, stagger=None, easing="ease-out-cubic"):
        op = {"type": "text-animate", "target": target, "preset": preset, "unit": unit,
              "start": int(start), "duration": int(duration), "easing": easing}
        if stagger is not None:
            op["stagger"] = int(stagger)
        self.ops.append(op)

    def breathe(self, target, start, end, period=2000, low=0.55):
        """The slow pulse a live status glow has in the interface."""
        frames, t, hi = [], start, True
        while t <= end:
            frames.append((t, 1 if hi else low))
            t += period / 2
            hi = not hi
        self.keys(target, "opacity", frames, "ease-in-out")

    # ---------------------------------------------------------------- build
    def project(self) -> Project:
        p = Project(W, H, self.background)
        p._workspace = str(ROOT)  # screenshot paths resolve against the repository root
        install_font(p, "Barlow", 700, role="heading")
        install_font(p, "Barlow", 400, role="body")
        install_font(p, "Barlow", 600)
        install_font(p, "Kode Mono", 500)
        p.apply([{"type": "swatch", "name": "accent", "color": TEAL},
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


# -------------------------------------------------------------------- primitives
def rect(name, x, y, w, h, fill, stroke=None, stroke_width=1):
    op = {"type": "shape", "shape": "rectangle", "name": name, "x": round(x, 2), "y": round(y, 2),
          "width": max(1, round(w)), "height": max(1, round(h)), "fill": fill}
    if stroke:
        op.update(stroke=stroke, stroke_width=stroke_width)
    return op


def ellipse(name, cx, cy, d, fill):
    return {"type": "shape", "shape": "ellipse", "name": name, "x": round(cx - d / 2, 2), "y": round(cy - d / 2, 2),
            "width": max(1, round(d)), "height": max(1, round(d)), "fill": fill}


def line(name, points, color, width=2):
    return {"type": "pen", "name": name, "points": [[round(x, 2), round(y, 2)] for x, y in points],
            "closed": False, "smooth": False, "stroke": color, "stroke_width": width}


def text(name, value, x, y, size, color=TEXT, font=BODY, **extra):
    return {"type": "text", "name": name, "text": value, "font": font, "size": size, "color": color,
            "x": round(x, 2), "y": round(y, 2), **extra}


def label(name, value, x, y, size=18, color=DIM, tracking=None):
    """The interface's panel label: Kode Mono, upper case, widely tracked."""
    return text(name, value.upper(), x, y, size, color, MONO, tracking=round(size * 0.28) if tracking is None
                else tracking)


def rich(name, spans, x, y, size, width, color=TEXT, font=HEAD, align="left", line_height=1.0):
    """Rich text; ``spans`` is a string or [(text, color or None), ...]."""
    if isinstance(spans, str):
        spans = [(spans, None)]
    out = [{"text": t, **({"color": c} if c else {})} for t, c in spans]
    return {"type": "rich-text", "name": name, "spans": out, "font": font, "size": size, "color": color,
            "x": round(x, 2), "y": round(y, 2), "width": round(width), "align": align, "line_height": line_height}


def glow(name, cx, cy, size, color, strength="55"):
    return {"type": "gradient", "name": name, "direction": "radial", "width": round(size), "height": round(size),
            "x": round(cx - size / 2), "y": round(cy - size / 2),
            "stops": [{"offset": 0, "color": f"{color}{strength}"}, {"offset": 0.45, "color": f"{color}18"},
                      {"offset": 1, "color": f"{color}00"}]}


def group(name, targets):
    return {"type": "group", "name": name, "targets": list(targets)}


def status_dot(name, cx, cy, color, d=14, halo=6):
    """A status dot with its soft glow (``{name}-glow``): glow means status, nothing else glows."""
    return [glow(f"{name}-glow", cx, cy, d * halo, color, "66"), ellipse(name, cx, cy, d, color)]


def tag(name, value, x, y, color, size=17, width=None):
    """A square status tag: the colour's wash behind its upper-case Kode Mono word."""
    w = width or round(len(value) * size * 0.72 + size * 1.4)
    h = round(size * 1.75)
    return [rect(f"{name}-bg", x, y, w, h, soft(color)),
            rich(f"{name}-label", value.upper(), x, y + (h - size * 1.2) / 2, size, w, color, MONO, "center", 1.2),
            group(name, [f"{name}-bg", f"{name}-label"])]


def panel(name, x, y, w, h, title, header=52, menu=True):
    """A dashboard widget: card surface, hairline border, a header band with its tracked label and the
    short accent tick the interface draws at a widget's top-left corner. Members are grouped as ``name``."""
    ops = [rect(f"{name}-bg", x, y, w, h, CARD, LINE_STRONG, 1),
           rect(f"{name}-edge", x + 1, y + 1, w - 2, 1, "#FFFFFF10"),
           rect(f"{name}-head", x + 1, y + header, w - 2, 1, LINE),
           rect(f"{name}-tick", x, y, 26, 2, TEAL),
           label(f"{name}-title", title, x + 24, y + (header - 18) / 2 - 2)]
    members = [f"{name}-bg", f"{name}-edge", f"{name}-head", f"{name}-tick", f"{name}-title"]
    if menu:
        ops.append(text(f"{name}-menu", "···", x + w - 48, y + (header - 26) / 2 - 4, 22, DIM, MONO))
        members.append(f"{name}-menu")
    return ops + [group(name, members)]


def grid(prefix="grid", step=48, color="#FFFFFF07", wash=(0.12, -0.1)):
    """The interface's backdrop: a faint square grid under a teal wash from the top-left corner."""
    ops = [rect(f"{prefix}-base", 0, 0, W, H, BG)]
    names = []
    for i, x in enumerate(range(step, W, step)):
        ops.append(rect(f"{prefix}-v{i}", x, 0, 1, H, color))
        names.append(f"{prefix}-v{i}")
    for i, y in enumerate(range(step, H, step)):
        ops.append(rect(f"{prefix}-h{i}", 0, y, W, 1, color))
        names.append(f"{prefix}-h{i}")
    ops.append(group(f"{prefix}-lines", names))
    ops.append({"type": "gradient", "name": f"{prefix}-wash", "direction": "radial", "width": 2200, "height": 1240,
                "x": round(W * wash[0] - 1100), "y": round(H * wash[1] - 620),
                "stops": [{"offset": 0, "color": "#43C9C01F"}, {"offset": 0.62, "color": "#43C9C000"},
                          {"offset": 1, "color": "#43C9C000"}]})
    return ops


def brackets(name, x, y, w, h, color=TEAL, arm=34, weight=3):
    """Four corner brackets: the acquisition reticle from web/signal-reticle.svg, squared off.
    Each corner is its own layer (``{name}-tl`` ...) so the reticle can close in or travel."""
    corners = {"tl": [(x, y + arm), (x, y), (x + arm, y)],
               "tr": [(x + w - arm, y), (x + w, y), (x + w, y + arm)],
               "br": [(x + w, y + h - arm), (x + w, y + h), (x + w - arm, y + h)],
               "bl": [(x + arm, y + h), (x, y + h), (x, y + h - arm)]}
    ops = [line(f"{name}-{k}", pts, color, weight) for k, pts in corners.items()]
    return ops + [group(name, [f"{name}-{k}" for k in corners])]


def glide(sc, name, box0, moves):
    """Move a reticle drawn at ``box0`` through ``moves`` [(time, (x, y, w, h), duration), ...]."""
    x0, y0, w0, h0 = box0
    frames = {k: {"translate-x": [(0, 0)], "translate-y": [(0, 0)]} for k in ("tl", "tr", "br", "bl")}
    for t, (x, y, w, h), dur in moves:
        deltas = {"tl": (x - x0, y - y0), "tr": (x + w - x0 - w0, y - y0),
                  "br": (x + w - x0 - w0, y + h - y0 - h0), "bl": (x - x0, y + h - y0 - h0)}
        for k, (dx, dy) in deltas.items():
            for prop, v in (("translate-x", dx), ("translate-y", dy)):
                last = frames[k][prop][-1][1]
                frames[k][prop] += [(t, last), (t + dur, round(v, 2))]
    for k, props in frames.items():
        for prop, fr in props.items():
            sc.keys(f"{name}-{k}", prop, fr, "ease-in-out-cubic")


SCREENS = {  # key: (repository-relative file, pixel width, pixel height)
    "dashboard": ("marketing/beta/screens/dashboard-dark.png", 2400, 1461),
    "map": ("marketing/beta/screens/map-dark.png", 2400, 1461),
    "wall": ("marketing/beta/screens/wallboard-dark.png", 2400, 1311),
    "discovery": ("marketing/video/screens/discovery-dark.png", 2400, 1461),
}


def screen(name, key, x=0, y=0, width=W):
    """A real interface screenshot, full-bleed and square: no window chrome, no rounding."""
    path, iw, ih = SCREENS[key]
    return {"type": "frame", "name": name, "path": path, "x": round(x), "y": round(y), "width": round(width),
            "height": round(width * ih / iw), "fit": "fill"}


def rail(name, height=H):
    """The interface's own left rail (logo and navigation icons), cropped from a real screenshot."""
    path, iw, ih = SCREENS["dashboard"]
    k = height / ih
    return [rect(f"{name}-win", 0, 0, round(86 * k), height, CARD),
            {"type": "frame", "name": f"{name}-img", "path": path, "x": 0, "y": 0, "width": round(iw * k),
             "height": round(height), "fit": "fill"},
            {"type": "clip", "target": f"{name}-img", "base": f"{name}-win"},
            rect(f"{name}-line", round(86 * k), 0, 1, height, LINE),
            group(name, [f"{name}-win", f"{name}-img", f"{name}-line"])], round(86 * k)


def mark(prefix, x, y, size, variant="dark"):
    """The GWatch mark from brand/ as editable layers: ``{prefix}-g``, ``-handle``, ``-iris``, ``-highlight``."""
    return beta.mark(prefix, x, y, size, variant)


def hms(h, m, s=0):
    return h * 3600 + m * 60 + s
