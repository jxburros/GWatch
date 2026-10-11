"""GWatch and GWatch Agent logo kits, drawn and exported with Vixl.

Run from anywhere, with Vixl 0.19+ installed (pip install 'vixl[pdf]'):

    python brand/build.py                  # both kits
    python brand/build.py gwatch           # one kit (gwatch or agent)

The vector geometry comes from source/geometry.json (made by source/trace.py from the supplied
artwork). Every file in a kit is rebuilt from scratch: editable .vixl masters plus SVG, PDF, PNG,
JPG, ICO and the platform icon sets.
"""

import json
import math
import os
import shutil
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
os.environ.setdefault("VIXL_NO_UPDATE", "1")

from PIL import Image, ImageDraw  # noqa: E402

from vixl import Project  # noqa: E402
from vixl.exports import export_icons  # noqa: E402
from vixl.typefaces import install_font  # noqa: E402

GEOMETRY = json.loads((HERE / "source" / "geometry.json").read_text())
WORDMARK_FONT = ("Barlow", 700)
FONT = "barlow-700"
LOGOS = {
    "gwatch": {"title": "GWatch", "words": [("GWatch", "navy")], "dir": "gwatch"},
    "agent": {"title": "GWatch Agent", "words": [("GWatch", "navy"), (" Agent", "accent")], "dir": "gwatch-agent"},
}
VARIANTS = ("color", "reverse", "black", "white")
# One-colour versions separate the iris from the pupil with a knockout ring this wide (source px).
KNOCKOUT = 13


# Geometry ------------------------------------------------------------------------------------
class Shape:
    """A path in document pixels, built from source-pixel geometry through a scale and offset."""

    def __init__(self, scale, ox, oy):
        self.s, self.ox, self.oy = scale, ox, oy
        self.parts, self.xs, self.ys = [], [], []

    def pt(self, x, y):
        x, y = x * self.s + self.ox, y * self.s + self.oy
        self.xs.append(x)
        self.ys.append(y)
        return x, y

    def contour(self, commands):
        for name, *values in commands:
            points = [self.pt(values[i], values[i + 1]) for i in range(0, len(values), 2)]
            self.parts.append((name, [v for p in points for v in p]))
        self.parts.append(("Z", []))
        return self

    def circle(self, cx, cy, r, clockwise=True):
        x0, y0 = self.pt(cx - r, cy)
        x1, y1 = self.pt(cx + r, cy)
        self.pt(cx, cy - r)
        self.pt(cx, cy + r)
        rr, sweep = r * self.s, 1 if clockwise else 0
        self.parts += [("M", [x0, y0]), ("A", [rr, rr, 0, 0, sweep, x1, y1]),
                       ("A", [rr, rr, 0, 0, sweep, x0, y0]), ("Z", [])]
        return self

    def op(self, name, fill):
        x, y = math.floor(min(self.xs)), math.floor(min(self.ys))
        width, height = math.ceil(max(self.xs)) - x, math.ceil(max(self.ys)) - y

        def fmt(v):
            return f"{v:.2f}".rstrip("0").rstrip(".")

        out = []
        for command, values in self.parts:
            if command == "A":
                rx, ry, rot, large, sweep, ex, ey = values
                values = [rx, ry, rot, large, sweep, ex - x, ey - y]
            else:
                values = [v - (x if i % 2 == 0 else y) for i, v in enumerate(values)]
            out.append(command + " ".join(fmt(v) for v in values))
        return {"type": "shape", "shape": "path", "name": name, "x": x, "y": y, "width": max(width, 1),
                "height": max(height, 1), "path": " ".join(out), "fill": fill}


def gwatch_g(shape):
    """The GWatch G as one contour: ring with its opening, plus the crossbar."""
    g = GEOMETRY["gwatch"]
    cx, cy, ro, ri = (g["ring"][k] for k in ("cx", "cy", "outer", "inner"))
    bar = g["bar"]
    theta = math.radians(g["opening_angle"])
    top = math.asin((bar["top"] - cy) / ro)  # where the outer edge meets the bar's top
    inner_x = cx + math.sqrt(ri * ri - (bar["bottom"] - cy) ** 2)
    a_out = (cx + ro * math.cos(theta), cy + ro * math.sin(theta))
    a_in = (cx + ri * math.cos(theta), cy + ri * math.sin(theta))
    s = shape.s
    shape.parts.append(("M", [*shape.pt(*a_out)]))
    shape.parts.append(("A", [ro * s, ro * s, 0, 1, 0, *shape.pt(cx + ro * math.cos(top), bar["top"])]))
    for corner in ((bar["x"], bar["top"]), (bar["x"], bar["bottom"]), (inner_x, bar["bottom"])):
        shape.parts.append(("L", [*shape.pt(*corner)]))
    shape.parts.append(("A", [ri * s, ri * s, 0, 1, 1, *shape.pt(*a_in)]))
    shape.parts.append(("Z", []))
    for x, y in ((cx - ro, cy - ro), (cx + ro, cy + ro)):  # full ring extent
        shape.pt(x, y)
    return shape


def gwatch_handle(shape):
    h = GEOMETRY["gwatch"]["handle"]
    (x1, y1), (x2, y2), r = h["from"], h["to"], h["radius"]
    length = math.hypot(x2 - x1, y2 - y1)
    nx, ny = -(y2 - y1) / length * r, (x2 - x1) / length * r
    rr = r * shape.s
    shape.parts.append(("M", [*shape.pt(x1 + nx, y1 + ny)]))
    shape.parts.append(("L", [*shape.pt(x2 + nx, y2 + ny)]))
    shape.parts.append(("A", [rr, rr, 0, 0, 0, *shape.pt(x2 - nx, y2 - ny)]))
    shape.parts.append(("L", [*shape.pt(x1 - nx, y1 - ny)]))
    shape.parts.append(("A", [rr, rr, 0, 0, 0, *shape.pt(x1 + nx, y1 + ny)]))
    shape.parts.append(("Z", []))
    for x, y in ((x2 - r, y2 - r), (x2 + r, y2 + r)):
        shape.pt(x, y)
    return shape


def artwork_bounds(logo):
    """Visible extent of a mark in source pixels: (left, top, right, bottom)."""
    if logo == "agent":
        points = [(v[i], v[i + 1]) for c in GEOMETRY["agent"]["navy"] for _, *v in c for i in range(0, len(v), 2)]
        xs, ys = zip(*points)
        return min(xs), min(ys), max(xs), max(ys)
    g = GEOMETRY["gwatch"]
    cx, cy, ro = g["ring"]["cx"], g["ring"]["cy"], g["ring"]["outer"]
    (hx, hy), r = g["handle"]["to"], g["handle"]["radius"]
    return cx - ro, cy - ro, max(cx + ro, hx + r), max(cy + ro, hy + r)


def palette(logo, variant):
    """Fills for each part of a mark; None leaves the part out (a transparent knockout)."""
    c = GEOMETRY[logo]["colors"]
    navy, accent, white = c["navy"], c["accent"], c["white"]
    if variant == "color":
        return {"body": navy, "disc": white, "iris": accent, "pupil": navy, "highlight": white, "accent": accent}
    if variant == "reverse":
        return {"body": white, "disc": None, "iris": accent, "pupil": None, "highlight": white, "accent": accent}
    ink = "#000000" if variant == "black" else "#FFFFFF"
    return {"body": ink, "disc": None, "iris": ink, "pupil": ink, "highlight": None, "accent": ink}


def mark_ops(logo, variant, scale, ox, oy):
    """Operations that draw a mark with its source-pixel origin at (ox, oy)."""
    fills = palette(logo, variant)
    geo = GEOMETRY[logo]

    def shape():
        return Shape(scale, ox, oy)

    ops = []
    if fills["disc"]:
        if logo == "agent":
            ops.append(shape().contour(geo["white"][0]).op("eye-white", fills["disc"]))
        else:
            ring = geo["ring"]
            ops.append(shape().circle(ring["cx"], ring["cy"], ring["inner"] + 2).op("eye-white", fills["disc"]))
    if logo == "agent":
        body = shape()
        for contour in geo["navy"]:
            body.contour(contour)
        ops.append(body.op("hat-and-g", fills["body"]))
    else:
        ops.append(gwatch_g(shape()).op("g", fills["body"]))
        ops.append(gwatch_handle(shape()).op("handle", fills["body"]))
    (ix, iy, ir), (px, py, pr), (hx, hy, hr) = geo["iris"], geo["pupil"], geo["highlight"]
    # The iris is a ring wherever the pupil is a knockout or the same colour as the iris.
    hole = None
    if fills["pupil"] is None:
        hole = pr
    elif fills["pupil"] == fills["iris"]:
        hole = pr + KNOCKOUT
    iris = shape().circle(ix, iy, ir)
    if hole:
        iris.circle(px, py, hole, clockwise=False)
    ops.append(iris.op("iris", fills["iris"]))
    if fills["pupil"]:
        pupil = shape().circle(px, py, pr)
        if not fills["highlight"]:
            pupil.circle(hx, hy, hr, clockwise=False)
        ops.append(pupil.op("pupil", fills["pupil"]))
    if fills["highlight"]:
        ops.append(shape().circle(hx, hy, hr).op("highlight", fills["highlight"]))
    return ops


def place(logo, box_x, box_y, box_w, box_h):
    """Scale and offset that fit a mark centred in a box."""
    left, top, right, bottom = artwork_bounds(logo)
    scale = min(box_w / (right - left), box_h / (bottom - top))
    ox = box_x + (box_w - (right - left) * scale) / 2 - left * scale
    oy = box_y + (box_h - (bottom - top) * scale) / 2 - top * scale
    return scale, ox, oy


# Documents -----------------------------------------------------------------------------------
def new(width, height, background="transparent", fonts=False):
    p = Project(width, height, background)
    if fonts:
        install_font(p, *WORDMARK_FONT)
    return p


def mark_doc(logo, variant, height=1000, pad=0.04):
    left, top, right, bottom = artwork_bounds(logo)
    inner = height * (1 - 2 * pad)
    scale = inner / (bottom - top)
    width = round((right - left) * scale + height * 2 * pad)
    margin = height * pad
    p = new(width, height)
    p.apply(mark_ops(logo, variant, *place(logo, margin, margin, width - 2 * margin, inner)), detail="brief")
    return p


def text_ops(logo, variant, x, y, size):
    fills = palette(logo, variant)
    colors = {"navy": fills["body"], "accent": fills["accent"]}
    words = LOGOS[logo]["words"]
    text = "".join(w for w, _ in words)
    op = {"type": "text", "name": "wordmark", "text": text, "x": x, "y": y, "font": FONT, "size": size,
          "color": colors[words[0][1]]}
    ops = [op]
    if len(words) > 1:
        start = len(words[0][0])
        ops.append({"type": "text-style", "target": "wordmark", "start": start, "end": len(text),
                    "color": colors[words[1][1]]})
    return ops


def text_ink(logo, size):
    """Rendered ink box (dx, dy, width, height) of the wordmark relative to the text layer origin."""
    probe = new(4000, 1000, fonts=True)
    probe.apply(text_ops(logo, "black", 100, 100, size), detail="brief")
    left, top, right, bottom = probe.render().getchannel("A").getbbox()
    return left - 100, top - 100, right - left, bottom - top


def lockup_doc(logo, variant, stacked):
    """Mark plus wordmark. Text is measured first, then the canvas is sized around both."""
    size = 300
    dx, dy, ink_w, ink_h = text_ink(logo, size)
    left, top, right, bottom = artwork_bounds(logo)
    if stacked:
        mark_h = 640
        mark_w = (right - left) * mark_h / (bottom - top)
        gap, pad = 60, 60
        width = round(max(mark_w, ink_w) + 2 * pad)
        height = round(mark_h + gap + ink_h + 2 * pad)
        p = new(width, height, fonts=True)
        p.apply(mark_ops(logo, variant, *place(logo, (width - mark_w) / 2, pad, mark_w, mark_h)), detail="brief")
        p.apply(text_ops(logo, variant, (width - ink_w) / 2 - dx, pad + mark_h + gap - dy, size), detail="brief")
    else:
        mark_h = round(ink_h * 2.1)
        mark_w = (right - left) * mark_h / (bottom - top)
        gap, pad = round(size * 0.32), 50
        width = round(mark_w + gap + ink_w + 2 * pad)
        height = round(mark_h + 2 * pad)
        p = new(width, height, fonts=True)
        p.apply(mark_ops(logo, variant, *place(logo, pad, pad, mark_w, mark_h)), detail="brief")
        p.apply(text_ops(logo, variant, pad + mark_w + gap - dx, (height - ink_h) / 2 - dy, size), detail="brief")
    return p


def tile_doc(logo, variant, background, fill=0.72, size=1024):
    """Square opaque tile: app icons and avatars. fill is the share of the side the mark may use."""
    p = new(size, size, background)
    box = size * fill
    p.apply(mark_ops(logo, variant, *place(logo, (size - box) / 2, (size - box) / 2, box, box)), detail="brief")
    return p


def favicon_doc(logo, variant, size=512):
    p = new(size, size)
    pad = size * 0.02
    p.apply(mark_ops(logo, variant, *place(logo, pad, pad, size - 2 * pad, size - 2 * pad)), detail="brief")
    return p


# Kit -----------------------------------------------------------------------------------------
def build(logo):
    out = HERE / LOGOS[logo]["dir"]
    if out.exists():
        shutil.rmtree(out)
    folders = {name: out / name for name in ("Editable-Vixl", "SVG", "PDF", "PNG", "Icons")}
    for folder in folders.values():
        folder.mkdir(parents=True)
    colors = GEOMETRY[logo]["colors"]
    written = []

    def save(p, stem):
        p.save(str(folders["Editable-Vixl"] / f"{stem}.vixl"))
        p.export(str(folders["SVG"] / f"{stem}.svg"), svg_policy="strict")
        written.append(stem)

    # Core logos: mark, horizontal and stacked in four colour treatments.
    for variant in VARIANTS:
        p = mark_doc(logo, variant)
        save(p, f"mark-{variant}")
        p.export(str(folders["PDF"] / f"mark-{variant}.pdf"))
        for height in (128, 256, 512, 1000, 2000):
            suffix = "" if height == 1000 else f"-{height}"
            p.export(str(folders["PNG"] / f"mark-{variant}{suffix}.png"), scale=height / 1000, alpha="keep")
        for layout, stacked in (("horizontal", False), ("stacked", True)):
            p = lockup_doc(logo, variant, stacked)
            stem = f"{layout}-{variant}"
            save(p, stem)
            p.export(str(folders["PDF"] / f"{stem}.pdf"))
            p.export(str(folders["PNG"] / f"{stem}.png"), alpha="keep")
            p.export(str(folders["PNG"] / f"{stem}@2x.png"), scale=2, alpha="keep")

    # Square tiles: app icons and social avatars, opaque.
    tiles = {
        "app-icon-light": (tile_doc(logo, "color", "#FFFFFF"), True),
        "app-icon-dark": (tile_doc(logo, "reverse", colors["navy"]), True),
        "avatar-light": (tile_doc(logo, "color", "#FFFFFF", fill=0.62), False),
        "avatar-dark": (tile_doc(logo, "reverse", colors["navy"], fill=0.62), False),
    }
    for stem, (p, platform_sets) in tiles.items():
        save(p, stem)
        p.export(str(folders["Icons"] / f"{stem}.png"))
        p.export(str(folders["Icons"] / f"{stem}.jpg"), quality=95)
        for px in (48, 96, 180, 192, 256, 512):
            p.export(str(folders["Icons"] / f"{stem}-{px}.png"), scale=px / 1024)
        if platform_sets:
            export_icons(p, str(out / "Icon-Sets" / stem), icon_set="all")

    # Favicons: transparent, the mark filling the square.
    for variant, stem in (("color", "favicon"), ("reverse", "favicon-dark")):
        p = favicon_doc(logo, variant)
        save(p, stem)
        sizes = [16, 24, 32, 48, 64, 128, 256]
        p.export(str(folders["Icons"] / f"{stem}.ico"), icon_sizes=sizes)
        p.export(str(folders["Icons"] / f"{stem}.png"), alpha="keep")
        for px in sizes:
            p.export(str(folders["Icons"] / f"{stem}-{px}.png"), scale=px / 512, alpha="keep")

    overview(logo, out)
    return written


def overview(logo, out):
    """Contact sheet of the core logos on light and dark panels."""
    navy = GEOMETRY[logo]["colors"]["navy"]
    rows = [("mark", 260), ("horizontal", 200), ("stacked", 260)]
    cell_w, pad = 720, 40
    sheet_w = cell_w * 4 + pad * 5
    sheet_h = sum(h + 2 * pad for _, h in rows) + pad
    sheet = Image.new("RGB", (sheet_w, sheet_h), "#F3F5F9")
    draw = ImageDraw.Draw(sheet)
    backgrounds = {"color": "#FFFFFF", "black": "#FFFFFF", "reverse": navy, "white": "#3A4150"}
    y = pad
    for layout, h in rows:
        for i, variant in enumerate(("color", "reverse", "black", "white")):
            x = pad + i * (cell_w + pad)
            draw.rounded_rectangle((x, y, x + cell_w, y + h + pad), radius=18, fill=backgrounds[variant])
            art = Image.open(out / "PNG" / f"{layout}-{variant}.png").convert("RGBA")
            art.thumbnail((cell_w - 2 * pad, h - pad // 2), Image.LANCZOS)
            sheet.paste(art, (x + (cell_w - art.width) // 2, y + (h + pad - art.height) // 2), art)
        y += h + 2 * pad
    sheet.save(out / "00-kit-overview.png")


def main(argv):
    chosen = argv or list(LOGOS)
    for logo in chosen:
        written = build(logo)
        print(f"{LOGOS[logo]['title']}: {len(written)} masters in {HERE / LOGOS[logo]['dir']}")


if __name__ == "__main__":
    main(sys.argv[1:])
