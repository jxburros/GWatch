"""GWatch Beta marketing kit, made with Vixl.

Run from anywhere, with Vixl 0.19+ installed (pip install 'vixl[pdf]' or a Vixl checkout with
`pip install -e '.[pdf]'`):

    node marketing/beta/screenshots.mjs        # refresh the interface screenshots (optional)
    python marketing/beta/build.py             # build everything
    python marketing/beta/build.py og deck     # build only some pieces

Every piece is written to marketing/beta/output/<folder>/ as an editable .vixl master, the
`vixl check` findings it shipped with (.check.json) and its exports. The GWatch mark is rebuilt
as editable vector paths from web/logo.svg and web/logo-dark.svg, and the background topology
comes from web/signal-field.svg, so the kit uses the product's own artwork.
"""

import json
import os
import re
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO_ROOT = HERE.parent.parent
OUT = HERE / "output"
os.environ.setdefault("VIXL_NO_UPDATE", "1")
os.chdir(HERE)  # frame paths (screens/...) resolve against this folder

from vixl import Project  # noqa: E402
from vixl.render import resolve_layout  # noqa: E402
from vixl.typefaces import install_font  # noqa: E402

# Brand, from web/app.css and web/logo*.svg ---------------------------------------------------
BG = "#0F1114"
CARD = "#16181D"
ELEV = "#22262D"
LINE = "#333941"
TEXT = "#F2F4F6"
MUTED = "#9EA7AF"
TEAL = "#43C9C0"
GOLD = "#D3A32E"
NAVY = "#061B3B"
UP = "#35E07F"
WARN = "#FFC542"
DOWN = "#FF5C5C"
PAPER = "#F4F6F8"
INK = "#0F1114"
SUBTLE = "#4F5862"
TEAL_INK = "#1F8F87"  # teal dark enough for text on paper

HEAD, BODY, SEMI, MONO = "barlow-800", "barlow-400", "barlow-600", "kode-mono-500"

# Facts used in the copy (from README.md, CHANGELOG.md and docs/ at 0.6.0; see README.md here).
VERSION = "0.6.0"
REPO = "github.com/jxburros/GWatch"
ISSUES = "github.com/jxburros/GWatch/issues"
CHECKS = ["Ping", "HTTP/S", "Certificates", "TCP port", "DNS", "Keyword", "JSON", "Custom script",
          "Hardware health", "SNMP"]
NOTIFY = ["Email", "Webhooks", "Slack", "Teams", "ntfy", "Pushover", "git", "Scripts"]
PLATFORMS = ["Windows", "Linux", "macOS", "Docker", "Raspberry Pi"]
TAGLINE = "Your home network, watched calmly."
SUB = ("GWatch checks your router, servers, websites and services on a schedule, keeps the history, "
       "and only speaks up when something actually needs you.")


# Helpers -------------------------------------------------------------------------------------
def new(width, height, background=BG):
    p = Project(width, height, background)
    install_font(p, "Barlow", 800, role="heading")
    install_font(p, "Barlow", 400, role="body")
    install_font(p, "Barlow", 600)
    install_font(p, "Kode Mono", 500)
    p.apply([{"type": "swatch", "name": "teal", "color": TEAL},
             {"type": "swatch", "name": "gold", "color": GOLD},
             {"type": "swatch", "name": "navy", "color": NAVY}], detail="brief")
    return p


def all_layers(p):
    stack, found = list(p.state["layers"]), []
    while stack:
        layer = stack.pop()
        found.append(layer)
        stack.extend(layer.get("children", []))
    return found


def bounds(p, name):
    ids = {layer["id"]: layer["name"] for layer in all_layers(p)}
    for ident, box in resolve_layout(p).items():
        if ids.get(ident) == name:
            return box
    raise KeyError(name)


def bottom(p, name):
    _, y, _, h = bounds(p, name)
    return y + h


def right(p, name):
    x, _, w, _ = bounds(p, name)
    return x + w


def move(p, name, x=None, y=None):
    bx, by, _, _ = bounds(p, name)
    p.apply({"type": "move", "target": name, "x": round(bx if x is None else x), "y": round(by if y is None else y)},
            detail="brief")


def center_x(p, name, x0, width):
    _, _, w, _ = bounds(p, name)
    move(p, name, x=x0 + (width - w) / 2)


def align_right(p, name, edge):
    _, _, w, _ = bounds(p, name)
    move(p, name, x=edge - w)


def text(name, value, x, y, size, color=MUTED, font=BODY, **extra):
    return {"type": "text", "name": name, "text": value, "font": font, "size": size, "color": color, "x": x, "y": y,
            **extra}


def para(name, value, x, y, size, width, color=MUTED, font=BODY, line_height=1.32, align="left"):
    return {"type": "rich-text", "name": name, "spans": [{"text": value}], "font": font, "size": size, "color": color,
            "line_height": line_height, "x": x, "y": y, "width": width, "align": align}


def headline(name, value, x, y, size, accent=None, color=TEXT, width=None, align="left", line_height=0.95,
             accent_color=TEAL):
    """Display type as rich text so the accent phrase can change colour and the leading stays tight."""
    spans = [{"text": value}]
    if accent and accent in value:
        before, after = value.split(accent, 1)
        spans = [s for s in ({"text": before}, {"text": accent, "color": accent_color}, {"text": after}) if s["text"]]
    op = {"type": "rich-text", "name": name, "spans": spans, "font": HEAD, "size": size, "color": color,
          "line_height": line_height, "align": align, "x": x, "y": y}
    if width:
        op["width"] = width
    return op


def rect(name, x, y, w, h, fill, radius=0, **extra):
    shape = "rounded-rectangle" if radius else "rectangle"
    op = {"type": "shape", "shape": shape, "name": name, "x": round(x), "y": round(y), "width": round(w),
          "height": round(h), "fill": fill, **extra}
    if radius:
        op["radius"] = radius
    return op


def dot(name, x, y, d, fill):
    return {"type": "shape", "shape": "ellipse", "name": name, "x": round(x), "y": round(y), "width": round(d),
            "height": round(d), "fill": fill}


def glow(name, x, y, size, color=TEAL, strength="40"):
    return {"type": "gradient", "name": name, "direction": "radial", "width": size, "height": size, "x": x, "y": y,
            "stops": [{"offset": 0, "color": f"{color}{strength}"}, {"offset": 0.6, "color": f"{color}0C"},
                      {"offset": 1, "color": f"{color}00"}]}


CROPPED = re.compile(r"(^|-)(glow|field|shot)(-|$)")  # decoration and screenshots that bleed off on purpose


def mark_crops(p):
    """Mark the active layer set's decoration and bleeding screenshots for `vixl check`.

    Pages and masters keep their own layers, so multi-page pieces call this before switching page."""
    names = sorted({layer["name"] for layer in all_layers(p) if CROPPED.search(layer.get("name") or "")})
    if names:
        p.apply([{"type": "layer-intent", "target": n, "allow_crop": True,
                  **({} if "shot" in n else {"role": "decoration"})} for n in names], detail="brief")


def save_and_export(p, folder, stem, exports, check=True, **check_options):
    mark_crops(p)
    folder = OUT / folder
    folder.mkdir(parents=True, exist_ok=True)
    p.save(str(folder / f"{stem}.vixl"))
    report = {}
    if check:
        result = p.check(**check_options)
        report = {"issues": [{k: i.get(k) for k in ("check", "severity", "action", "layer", "message")}
                             for i in result.get("issues", [])]}
        fixes = [i for i in report["issues"] if i["action"] == "fix"]
        print(f"  check {stem}: {len(report['issues'])} issues, {len(fixes)} to fix")
        for issue in report["issues"]:
            print("   ", issue["action"].upper(), issue["check"], (issue["message"] or "")[:150])
        (folder / f"{stem}.check.json").write_text(json.dumps(report, indent=2) + "\n")
    for ext, options in exports:
        path = folder / f"{stem}.{ext}"
        p.export(str(path), **options)
        print("  wrote", path.relative_to(REPO_ROOT))
    return report


def sheet(p, path, **options):
    from vixl.deck import contact_sheet

    contact_sheet(p, **options).convert("RGB").save(path)
    print("  wrote", Path(path).relative_to(REPO_ROOT))


# The GWatch mark, as editable paths -----------------------------------------------------------
MARK_COLOURS = {  # sclera, iris, pupil, glint, ring: web/logo.svg and web/logo-dark.svg
    "light": ("#FDFDFD", GOLD, NAVY, "#FFFFFF", NAVY),
    "dark": ("#1B1E24", GOLD, TEXT, BG, TEXT),
    "teal": ("#FDFDFD", GOLD, NAVY, "#FFFFFF", TEAL),
}


def _circle(cx, cy, r, k):
    a, b, rr, yy = (cx - r) * k, (cx + r) * k, r * k, cy * k
    return f"M{a:.2f} {yy:.2f} A{rr:.2f} {rr:.2f} 0 1 0 {b:.2f} {yy:.2f} A{rr:.2f} {rr:.2f} 0 1 0 {a:.2f} {yy:.2f} Z"


def mark(prefix, x, y, size, variant="dark"):
    """The magnifier-G with its eye, scaled from the logo's 128-unit view box."""
    sclera, iris, pupil, glint, ring = MARK_COLOURS[variant]
    k = size / 128
    ops = []
    for i, (cx, cy, r, c) in enumerate([(55.8, 55.8, 39.5, sclera), (56.8, 56.4, 22.9, iris),
                                        (56.9, 56.9, 12.7, pupil), (62.8, 51.6, 3.6, glint)]):
        ops.append({"type": "shape", "shape": "path", "name": f"{prefix}-c{i}", "x": round(x), "y": round(y),
                    "width": round(size), "height": round(size), "path": _circle(cx, cy, r, k), "fill": c})
    arc = 45.46 * k
    strokes = [(f"M{101.2 * k:.2f} {53.42 * k:.2f} A{arc:.2f} {arc:.2f} 0 1 1 {95.17 * k:.2f} {33.07 * k:.2f}", 12.37),
               (f"M{88.9 * k:.2f} {58.81 * k:.2f} L{101.6 * k:.2f} {58.81 * k:.2f}", 11.44),
               (f"M{94.46 * k:.2f} {94.62 * k:.2f} L{115.57 * k:.2f} {115.88 * k:.2f}", 16.24)]
    for i, (d, w) in enumerate(strokes):
        ops.append({"type": "shape", "shape": "path", "name": f"{prefix}-s{i}", "x": round(x), "y": round(y),
                    "width": round(size), "height": round(size), "path": d, "fill": "#00000000", "stroke": ring,
                    "stroke_width": max(1, round(w * k)), "line_cap": "round"})
    ops.append({"type": "group", "name": prefix, "targets": [o["name"] for o in ops]})
    return ops


def lockup(p, prefix, x, y, height, variant="dark", word_color=None):
    """Mark plus the GWatch wordmark set in Barlow, as the README and installer show it."""
    word_color = word_color or (TEXT if variant != "light" else INK)
    p.apply(mark(f"{prefix}-mark", x, y, height, variant), detail="brief")
    p.apply(text(f"{prefix}-word", "GWatch", x + height * 1.08, y, round(height * 0.62), word_color, HEAD),
            detail="brief")
    _, _, _, wh = bounds(p, f"{prefix}-word")
    move(p, f"{prefix}-word", y=round(y + height * 0.44 - wh / 2))  # centre the ink on the eye
    p.apply({"type": "group", "name": prefix, "targets": [f"{prefix}-mark", f"{prefix}-word"]}, detail="brief")


# Background topology, from web/signal-field.svg ----------------------------------------------
def _field_geometry():
    svg = (REPO_ROOT / "web" / "signal-field.svg").read_text()
    lines = []
    for opacity, d in re.findall(r'<path opacity="([\d.]+)" d="([^"]+)"', svg):
        pts, cx, cy = [], 0.0, 0.0
        for cmd, args in re.findall(r"([MLHVhlv])([^MLHVhlv]*)", d):
            nums = [float(n) for n in re.findall(r"-?\d*\.?\d+", args)]
            if cmd == "M":
                cx, cy = nums
            elif cmd == "h":
                cx += nums[0]
            elif cmd == "H":
                cx = nums[0]
            elif cmd == "v":
                cy += nums[0]
            elif cmd == "V":
                cy = nums[0]
            elif cmd == "l":
                cx, cy = cx + nums[0], cy + nums[1]
            elif cmd == "L":
                cx, cy = nums
            pts.append((cx, cy))
        lines.append((float(opacity), pts))
    dots = [(float(cx), float(cy), float(r), float(o)) for cx, cy, r, o in
            re.findall(r'<circle cx="([\d.]+)" cy="([\d.]+)" r="([\d.]+)" opacity="([\d.]+)"', svg)]
    return lines, dots


FIELD = _field_geometry()


def field(prefix, width, height, color=TEAL, strength=1.0, x=0, y=0, stroke=2, dot_scale=1.6):
    """The product's decorative topology field, scaled to cover width x height."""
    k = max(width / 1440, height / 900)
    ops = []
    lines, dots = FIELD
    for i, (opacity, pts) in enumerate(lines):
        d = "M" + " L".join(f"{px * k:.1f} {py * k:.1f}" for px, py in pts)
        alpha = max(8, min(255, round(opacity * 255 * strength * 1.4)))
        ops.append({"type": "shape", "shape": "path", "name": f"{prefix}-l{i}", "x": x, "y": y,
                    "width": round(1440 * k), "height": round(900 * k), "path": d, "fill": "#00000000",
                    "stroke": f"{color}{alpha:02X}", "stroke_width": stroke})
    for i, (cx, cy, r, opacity) in enumerate(dots):
        if cx * k > width or cy * k > height:
            continue
        rr = r * k * dot_scale / 1.6 + 1
        alpha = max(10, min(255, round(opacity * 255 * strength * 1.3)))
        ops.append(dot(f"{prefix}-d{i}", x + cx * k - rr, y + cy * k - rr, 2 * rr, f"{color}{alpha:02X}"))
    ops.append({"type": "group", "name": prefix, "targets": [o["name"] for o in ops]})
    return ops


# Interface screenshots, framed as an app window ----------------------------------------------
SCREENS = {  # key: (file, width, height) — see screenshots.mjs
    "dashboard": ("screens/dashboard-dark.png", 2400, 1461),
    "dashboard-light": ("screens/dashboard-light.png", 2400, 1461),
    "map": ("screens/map-dark.png", 2400, 1461),
    "map-light": ("screens/map-light.png", 2400, 1461),
    "node": ("screens/node-dark.png", 2400, 1461),
    "nodes": ("screens/nodes-dark.png", 2400, 1461),
    "charts": ("screens/charts-dark.png", 2400, 1461),
    "wall": ("screens/wallboard-dark.png", 2400, 1311),
}


def screen(p, name, key, x, y, width, height=None, chrome=True, light=False, crop_y=0, shadow=True):
    """A screenshot in a rounded window with a title bar; height defaults to the image's aspect."""
    path, iw, ih = SCREENS[key]
    bar = round(width * 0.028) if chrome else 0
    image_h = round(width * ih / iw)
    h = height or (image_h + bar)
    frame_fill = "#E3E7EC" if light else "#1B1E24"
    ops = [rect(f"{name}-base", x, y, width, h, frame_fill, radius=max(8, round(width * 0.012)),
                stroke="#FFFFFF1A" if not light else "#C9D0D8", stroke_width=2)]
    if chrome:
        r = max(4, round(bar * 0.32))
        for i, c in enumerate(["#FF5F57", "#FEBC2E", "#28C840"]):
            ops.append(dot(f"{name}-tl{i}", x + bar * 0.6 + i * r * 2.6, y + bar / 2 - r, 2 * r, c))
    ops += [rect(f"{name}-win", x, y + bar, width, h - bar, BG),
            {"type": "frame", "name": f"{name}-img", "path": path, "x": round(x), "y": round(y + bar - crop_y),
             "width": round(width), "height": image_h, "fit": "fill"},
            {"type": "clip", "target": f"{name}-img", "base": f"{name}-win"}]
    p.apply(ops, detail="brief")
    if shadow:
        p.apply({"type": "look", "target": f"{name}-base", "look": "soft-shadow", "color": "#000000",
                 "amount": 0.55 if not light else 0.25}, detail="brief")
    p.apply({"type": "group", "name": name, "targets": [o["name"] for o in ops if o.get("name") and
                                                        o["type"] != "clip"]}, detail="brief")
    return h


def chip(p, name, label, x, y, size=22, fg=TEXT, bg=ELEV, stroke=None, pad=(18, 10), font=SEMI, dot_color=None):
    """Pill label: the text is measured first, then a capsule is centred behind it."""
    lead = round(size * 0.85) if dot_color else 0
    p.apply(text(f"{name}-label", label, x + pad[0] + lead, y, size, fg, font), detail="brief")
    _, ly, lw, _ = bounds(p, f"{name}-label")
    cap = size * 0.70
    w, h = round(lw + 2 * pad[0] + lead), round(cap + 2 * pad[1] + size * 0.3)
    bg_op = rect(f"{name}-bg", x, y, w, h, bg, radius=h / 2)
    if stroke:
        bg_op.update(stroke=stroke, stroke_width=2)
    top_gap = ly - y
    ops = [bg_op, {"type": "move", "target": f"{name}-label", "x": x + pad[0] + lead,
                   "y": round(y + (h - cap) / 2 - top_gap)},
           {"type": "lower", "target": f"{name}-bg"}]
    targets = [f"{name}-bg", f"{name}-label"]
    if dot_color:
        d = round(size * 0.42)
        ops.append(dot(f"{name}-dot", x + pad[0], y + (h - d) / 2, d, dot_color))
        targets.append(f"{name}-dot")
    ops.append({"type": "group", "name": name, "targets": targets})
    p.apply(ops, detail="brief")
    return x, y, w, h


def chips_row(p, prefix, labels, x, y, gap=12, max_x=None, line_gap=12, **kw):
    cx, cy, boxes = x, y, []
    for i, label in enumerate(labels):
        box = chip(p, f"{prefix}-{i}", label, cx, cy, **kw)
        if max_x and cx + box[2] > max_x and cx > x:  # wrap onto the next line
            cy += box[3] + line_gap
            cx = x
            move(p, f"{prefix}-{i}", x=cx, y=cy)
            box = (cx, cy, box[2], box[3])
        boxes.append(box)
        cx += box[2] + gap
    return boxes


def status_strip(prefix, x, y, size=14, gap=10):
    """Up, degraded, down: the three status colours as a tiny signal row."""
    return [dot(f"{prefix}-{i}", x + i * (size + gap), y, size, c) for i, c in enumerate([UP, WARN, DOWN])]


def beta_badge(p, name, x, y, size=24, light=False):
    return chip(p, name, f"BETA  {VERSION}", x, y, size, NAVY if not light else TEXT, GOLD if not light else NAVY,
                font=MONO, pad=(round(size * 0.8), round(size * 0.45)))


JOIN_STEPS = [("Install 0.6.0", "Windows installer, Docker image or Linux/macOS build, signed and free."),
              ("Watch your real network", "Your router, NAS, Pi-hole, websites and APIs: whatever you run."),
              ("Tell us everything", "Bugs, rough edges and the features you wish it had, on GitHub Issues.")]

FEATURES = [("Checks everything", "Ping, HTTP/S, certificates, ports, DNS, keywords, JSON, SNMP, hardware health "
             "and your own scripts."),
            ("Alerts that don't nag", "After N failures, on recovery, with cooldowns, silences, maintenance windows "
             "and dependency-aware quiet."),
            ("Sees the whole picture", "A live network map traces what an outage really takes down, beneath the "
             "device that failed."),
            ("Remembers", "Long-term history, charts that stack today over last week, incidents and availability "
             "reports."),
            ("Acts for you", "Email, Slack, Teams, ntfy, Pushover, webhooks, git and scripts when something "
             "changes."),
            ("Stays home", "No cloud account, nothing phoned home, never internet-facing on its own. Signed updates, "
             "encrypted backups.")]


# Pieces --------------------------------------------------------------------------------------
def build_og():
    """Open Graph / link card, 1200 x 630."""
    W, H = 1200, 630
    p = new(W, H)
    p.apply([glow("glow", -260, -380, 1100), *field("field", W, H, strength=0.7)], detail="brief")
    lockup(p, "logo", 64, 52, 56)
    beta_badge(p, "badge", 0, 60, 20)
    align_right(p, "badge", W - 64)
    p.apply([headline("head", "Your home\nnetwork,\nwatched calmly.", 64, 150, 58, "watched calmly."),
             para("sub", "A calm, local monitor for your router, servers, websites and services. "
                  "Join the beta and help shape 1.0.", 64, 0, 24, 540, MUTED)], detail="brief")
    move(p, "sub", y=bottom(p, "head") + 26)
    chips_row(p, "tag", ["Free to use", "No cloud account", "Runs at home"], 64, bottom(p, "sub") + 28, size=20,
              dot_color=UP)
    screen(p, "shot", "dashboard", 660, 168, 720, height=420)
    save_and_export(p, "social", "og-card-1200x630", [("png", {})], thumbnail_width=600)


def build_github():
    """GitHub social preview, 1280 x 640, in the light theme."""
    W, H = 1280, 640
    p = new(W, H, PAPER)
    p.apply([*field("field", W, H, TEAL_INK, strength=0.5)], detail="brief")
    lockup(p, "logo", 64, 60, 60, "light")
    p.apply([headline("head", "Know the moment\nsomething breaks.\nAnd only then.", 64, 182, 58, "And only then.", INK,
                      accent_color=TEAL_INK, width=560)], detail="brief")
    p.apply([para("sub", "Calm monitoring for your home network and its services. Now in beta.", 64,
                  bottom(p, "head") + 26, 24, 520, SUBTLE)], detail="brief")
    chips_row(p, "tag", ["Windows", "Linux", "macOS", "Docker"], 64, bottom(p, "sub") + 28, size=20, fg=INK,
              bg="#FFFFFF", stroke="#D3D9E0")
    screen(p, "shot", "map-light", 660, 120, 760, height=440, light=True)
    beta_badge(p, "badge", 0, 62, 20, light=True)
    align_right(p, "badge", W - 64)
    save_and_export(p, "social", "github-preview-1280x640", [("png", {})], thumbnail_width=640)


def build_square():
    """Square post, 1080 x 1080, for X, Mastodon, Bluesky, Instagram and LinkedIn."""
    W = H = 1080
    p = new(W, H)
    M = 80
    p.apply([glow("glow", 300, -500, 1400), *field("field", W, H, strength=0.8)], detail="brief")
    lockup(p, "logo", M, 72, 64)
    beta_badge(p, "badge", 0, 84, 22)
    align_right(p, "badge", W - M)
    p.apply([headline("head", "Join the\nGWatch Beta.", M, 200, 120, "GWatch Beta.")], detail="brief")
    p.apply([para("sub", "Calm, local monitoring for everything on your network. Be one of the first to run "
                  "it, and help shape 1.0.", M, bottom(p, "head") + 30, 32, W - 2 * M, MUTED)], detail="brief")
    top = bottom(p, "sub") + 50
    screen(p, "shot", "wall", M, top, W - 2 * M + 200, height=H - top + 40, shadow=True)
    save_and_export(p, "social", "square-1080x1080", [("png", {})], thumbnail_width=540)


def build_story():
    """Vertical story, 1080 x 1920 (top and bottom 250 px kept clear of platform UI)."""
    W, H = 1080, 1920
    p = new(W, H)
    M = 80
    p.apply([glow("glow", -400, 700, 1700, TEAL, "38"), *field("field", W, H, strength=0.8)], detail="brief")
    lockup(p, "logo", M, 270, 72)
    p.apply([headline("head", "Your network,\nwatched\ncalmly.", M, 390, 120, "calmly.")], detail="brief")
    top = bottom(p, "head") + 56
    top += screen(p, "shot", "map", M, top, W - M, height=500) + 48  # child bounds are group-relative
    beta_badge(p, "badge", M, top, 26)
    p.apply([para("cta", "Run it, break it, tell us what you'd love next.", M, top + 76, 44, W - 2 * M,
                  TEXT, SEMI, line_height=1.2)], detail="brief")
    p.apply([text("url", REPO, M, bottom(p, "cta") + 24, 30, TEAL, MONO)], detail="brief")
    save_and_export(p, "social", "story-1080x1920", [("png", {})], thumbnail_width=540,
                    safe_area={"left": 60, "right": 60, "top": 250, "bottom": 250})


def build_banner():
    """Email / newsletter / forum header, 1200 x 400."""
    W, H = 1200, 400
    p = new(W, H)
    p.apply([glow("glow", 520, -500, 1100), *field("field", W, H, strength=0.8)], detail="brief")
    lockup(p, "logo", 64, 64, 56)
    p.apply([headline("head", "You're invited to\nthe GWatch Beta.", 64, 150, 58, "the GWatch Beta.")],
            detail="brief")
    screen(p, "shot", "dashboard", 720, 64, 600, height=372)
    beta_badge(p, "badge", 64, 0, 20)
    move(p, "badge", y=bottom(p, "head") + 24)
    save_and_export(p, "social", "email-header-1200x400", [("png", {})], thumbnail_width=600)


def build_carousel():
    """Six-slide carousel, 1080 x 1350: PDF plus one PNG per slide."""
    W, H = 1080, 1350
    p = new(W, H)
    M = 88
    p.apply([{"type": "master", "action": "add", "name": "frame", "background": BG},
             *field("m-field", W, H, strength=0.55),
             text("m-num", "${page} / ${pages}", W - M - 70, H - 100, 24, MUTED, MONO)], detail="brief")
    p.apply(mark("m-mark", M, H - 116, 44), detail="brief")

    def page(name):
        mark_crops(p)  # the master or previous page is still the active layer set
        p.apply({"type": "page", "action": "add", "name": name, "master": "frame"}, detail="brief")

    def title(prefix, kicker, head, accent, y=150, size=96):
        p.apply([text(f"{prefix}-kicker", kicker, M, y, 26, TEAL, MONO),
                 headline(f"{prefix}-head", head, M, y + 64, size, accent)], detail="brief")
        return bottom(p, f"{prefix}-head")

    # 1 - cover
    page("cover")
    p.apply([glow("c-glow", -300, -200, 1500, TEAL, "44")], detail="brief")
    p.apply(mark("c-mark", W - 470, 140, 380, "dark"), detail="brief")
    beta_badge(p, "c-badge", M, 170, 26)
    p.apply([headline("c-head", "Your\nnetwork,\nwatched\ncalmly.", M, 400, 132, "calmly.")], detail="brief")
    p.apply([para("c-sub", "Meet GWatch, and join the beta. Swipe to see what it does.", M, bottom(p, "c-head") + 40,
                  34, 760, MUTED)], detail="brief")

    # 2 - what it is
    page("what")
    y = title("w", "WHAT IT IS", "One calm place\nfor everything on\nyour network.", "everything")
    pts = [("It checks", "Router, servers, websites, APIs, certificates and hardware, on a schedule."),
           ("It remembers", "Years of history in one small database, rolled up so it never bloats."),
           ("It speaks up only when it should", "No alert storms: thresholds, cooldowns, maintenance and "
            "dependencies.")]
    ops = []
    for i, (t, d) in enumerate(pts):
        yy = y + 90 + i * 200
        ops += [dot(f"w-dot{i}", M, yy + 12, 24, [UP, WARN, TEAL][i]),
                text(f"w-t{i}", t, M + 56, yy, 44, TEXT, HEAD),
                para(f"w-d{i}", d, M + 56, yy + 64, 32, 820, MUTED)]
    p.apply(ops, detail="brief")

    # 3 - dashboard
    page("glance")
    y = title("g", "THE DASHBOARD", "Everything,\nat a glance.", "at a glance.")
    screen(p, "g-shot", "dashboard", M, y + 70, 1240, height=600)
    p.apply([para("g-cap", "The real interface, showing its built-in demo network.", M, y + 710, 28, W - 2 * M, MUTED)],
            detail="brief")

    # 4 - network map
    page("map")
    y = title("m", "THE NETWORK MAP", "See what an\noutage really hits.", "really hits.")
    screen(p, "m-shot", "map", M, y + 70, 1240, height=600)
    p.apply([para("m-cap", "Every node sits under the one it depends on, so when the gateway drops, you "
                  "get one alert, not eleven.", M, y + 710, 28, W - 2 * M, MUTED)], detail="brief")

    # 5 - in the box
    page("box")
    y = title("b", "IN THE BOX", "Small to install.\nBig on what\nit does.", "Big")
    stats = [("10", "check types"), ("8", "ways to notify"), ("3", "database choices"), ("5", "places it runs"),
             ("0", "cloud accounts"), ("100%", "signed releases")]
    ops = []
    for i, (n, label) in enumerate(stats):
        cx, cy = M + (i % 2) * 460, y + 80 + (i // 2) * 200
        ops += [text(f"b-v{i}", n, cx, cy, 116, TEAL if i in (0, 4) else TEXT, HEAD),
                text(f"b-l{i}", label, cx + 4, cy + 132, 34, MUTED)]
    p.apply(ops, detail="brief")

    # 6 - join
    page("join")
    p.apply([glow("j-glow", 100, 400, 1300, TEAL, "50")], detail="brief")
    y = title("j", "THE BETA IS OPEN", "Run it early.\nShape 1.0.", "Shape 1.0.")
    ops = []
    for i, (t, d) in enumerate(JOIN_STEPS):
        yy = y + 80 + i * 190
        ops += [rect(f"j-card{i}", M, yy, W - 2 * M, 166, CARD, radius=14, stroke="#FFFFFF14", stroke_width=2),
                text(f"j-n{i}", f"0{i + 1}", M + 36, yy + 34, 40, GOLD, HEAD),
                text(f"j-t{i}", t, M + 130, yy + 28, 40, TEXT, HEAD),
                para(f"j-d{i}", d, M + 130, yy + 86, 26, W - 2 * M - 170, MUTED)]
    p.apply(ops, detail="brief")
    p.apply([text("j-url", REPO, M, y + 680, 40, TEXT, SEMI)], detail="brief")

    report = save_and_export(p, "carousel", "carousel-1080x1350", [("pdf", {})], thumbnail_width=540,
                             checks=["bounds", "overlap", "contrast", "fonts", "deck"])
    names = ["cover", "what", "glance", "map", "box", "join"]
    for i, name in enumerate(names, 1):
        p.export(str(OUT / "carousel" / f"slide-{i}-{name}.png"), page=name)
    sheet(p, OUT / "carousel" / "contact-sheet.png", width=360, columns=6)
    return report


def build_flyer():
    """Letter-size beta invitation: what GWatch is, why it's good, how to join. Vector PDF."""
    p = new(100, 100, "#FFFFFF")
    p.apply({"type": "canvas", "size": "letter", "dpi": 150, "background": "#FFFFFF"}, detail="brief")
    W, H = p.state["canvas"]["width"], p.state["canvas"]["height"]
    M = 90
    p.apply([rect("band", 0, 0, W, 760, BG), glow("glow", -300, -500, 1300, TEAL, "40"),
             *field("field", W, 760, strength=0.7)], detail="brief")
    lockup(p, "logo", M, 64, 60)
    beta_badge(p, "badge", 0, 76, 18)
    align_right(p, "badge", W - M)
    p.apply([headline("head", "Your home network,\nwatched calmly.", M, 180, 66, "watched calmly.")], detail="brief")
    p.apply([para("sub", SUB + " You're invited to run the beta before anyone else.", M, bottom(p, "head") + 24,
                  23, W - 2 * M - 60, MUTED)], detail="brief")
    top = bottom(p, "sub") + 36
    shot_h = screen(p, "shot", "dashboard", M, top, W - 2 * M, height=330)
    band_h = top + 200
    p.apply({"type": "resize", "target": "band", "height": band_h}, detail="brief")
    top = top + shot_h + 44
    cw = (W - 2 * M - 50) / 2
    ops = []
    for i, (t, d) in enumerate(FEATURES):
        cx, cy = round(M + (i % 2) * (cw + 50)), top + (i // 2) * 132
        ops += [dot(f"f-dot{i}", cx, cy + 9, 14, [UP, WARN, DOWN, TEAL_INK, GOLD, NAVY][i]),
                text(f"f-t{i}", t, cx + 30, cy, 25, INK, HEAD),
                para(f"f-d{i}", d, cx + 30, cy + 38, 18, round(cw - 30), SUBTLE, line_height=1.35)]
    p.apply(ops, detail="brief")
    top = max(bottom(p, "f-d4"), bottom(p, "f-d5")) + 44
    box_h = 236
    p.apply([rect("join-bg", M, top, W - 2 * M, box_h, "#EEF7F6", radius=14, stroke="#BFE3E0", stroke_width=2),
             text("join-h", "Join the beta in three steps", M + 36, top + 28, 28, INK, HEAD)], detail="brief")
    sw = (W - 2 * M - 72 - 40) / 3
    ops = []
    for i, (t, d) in enumerate(JOIN_STEPS):
        cx = round(M + 36 + i * (sw + 20))
        ops += [text(f"j-n{i}", f"0{i + 1}", cx, top + 84, 30, TEAL_INK, HEAD),
                text(f"j-t{i}", t, cx + 48, top + 88, 20, INK, HEAD),
                para(f"j-d{i}", d, cx, top + 130, 17, round(sw), SUBTLE, line_height=1.35)]
    p.apply(ops, detail="brief")
    p.apply([rect("foot-rule", M, H - 112, W - 2 * M, 2, "#DDE2E8"),
             text("foot-url", REPO, M, H - 88, 22, INK, MONO),
             text("foot-legal", "GWatch is published by JX Holdings, LLC.", M, H - 88, 16, SUBTLE)],
            detail="brief")
    align_right(p, "foot-legal", W - M)
    move(p, "foot-legal", y=H - 84)
    save_and_export(p, "print", "beta-flyer-letter", [("pdf", {}), ("png", {"scale": 0.6})])


def build_poster():
    """Tabloid (11 x 17 in) poster with bleed: RGB PNG preview and a CMYK PDF for print."""
    p = new(100, 100)
    p.apply({"type": "canvas", "size": "tabloid", "dpi": 150, "bleed": True, "background": BG}, detail="brief")
    c = p.state["canvas"]
    W, H = c["width"], c["height"]
    M = 150
    p.apply([glow("glow", -700, -600, 2600, TEAL, "40"), *field("field", W, H, strength=0.8, stroke=3)],
            detail="brief")
    lockup(p, "logo", M, 160, 110)
    beta_badge(p, "badge", 0, 186, 34)
    align_right(p, "badge", W - M)
    p.apply([headline("head", "CALM IS\nA FEATURE.", M, 400, 220, "FEATURE.", line_height=0.88)], detail="brief")
    top = bottom(p, "head") + 60
    p.apply([para("sub", "GWatch watches your router, servers, websites and services around the clock, and only "
                  "speaks up when something actually needs you. Local, private, free to use.", M, top, 46,
                  W - 2 * M - 40, MUTED, line_height=1.32)], detail="brief")
    shot_top = bottom(p, "sub") + 70
    top = H - 300 - 90 - 260  # the columns sit above the call to action
    natural = round((W - 2 * M) * 1311 / 2400 + (W - 2 * M) * 0.028)
    screen(p, "shot", "wall", M, shot_top, W - 2 * M, height=min(natural, top - 80 - shot_top))
    cols = [("Checks everything", "Ping, HTTP/S, DNS, SNMP, certificates, hardware and your own scripts."),
            ("Alerts with manners", "Cooldowns, maintenance windows and dependency-aware quiet."),
            ("Stays home", "No cloud account. Nothing phoned home. Signed updates.")]
    cw = (W - 2 * M - 80) / 3
    ops = []
    for i, (t, d) in enumerate(cols):
        cx = round(M + i * (cw + 40))
        ops += [rect(f"col-rule{i}", cx, top, cw, 6, [UP, WARN, TEAL][i]),
                text(f"col-t{i}", t, cx, top + 40, 46, TEXT, HEAD),
                para(f"col-d{i}", d, cx, top + 112, 32, round(cw), MUTED)]
    p.apply(ops, detail="brief")
    p.apply([text("cta", "Join the beta:", M, H - 290, 54, TEXT, HEAD),
             text("url", REPO, M, H - 210, 48, TEAL, MONO)], detail="brief")
    save_and_export(p, "print", "poster-tabloid", [("png", {"scale": 0.5}), ("pdf", {"color_space": "cmyk"})],
                    checks=["bounds", "overlap", "contrast", "safe_area", "fonts", "print"])


def build_deck():
    """Nine-slide beta briefing deck, 1920 x 1080: PDF, editable PPTX and an HTML presenter."""
    W, H = 1920, 1080
    p = new(W, H)
    M = 140
    p.apply([{"type": "master", "action": "add", "name": "std", "background": BG},
             *field("m-field", W, H, strength=0.45),
             text("m-num", "${page}", W - M - 30, H - 96, 24, MUTED, MONO)], detail="brief")
    p.apply(mark("m-mark", M, H - 120, 48), detail="brief")

    def slide(name, notes, master="std"):
        mark_crops(p)  # the master or previous slide is still the active layer set
        p.apply({"type": "page", "action": "add", "name": name, "master": master, "notes": notes,
                 "transition": "fade"}, detail="brief")

    def title(prefix, kicker, head, accent=None, y=140, size=88):
        p.apply([text(f"{prefix}-kicker", kicker, M, y, 26, TEAL, MONO),
                 headline(f"{prefix}-title", head, M, y + 56, size, accent)], detail="brief")
        return bottom(p, f"{prefix}-title")

    slide("title", "GWatch is a calm monitor for a home network and its services. We're opening a beta "
          "before the 1.0 public release, and this is the invitation.", master=None)
    p.apply([glow("t-glow", -400, -500, 2000, TEAL, "44"), *field("t-field", W, H, strength=0.8)], detail="brief")
    p.apply(mark("t-mark", W - 700, 200, 560, "dark"), detail="brief")
    lockup(p, "t-logo", M, 150, 84)
    p.apply([headline("t-title", "Your home network,\nwatched calmly.", M, 380, 112, "watched calmly.")],
            detail="brief")
    beta_badge(p, "t-badge", M, bottom(p, "t-title") + 70, 30)
    p.apply([text("t-sub", "You're invited to the beta.", 0, 0, 36, MUTED)], detail="brief")
    _, by, bw, bh = bounds(p, "t-badge")
    move(p, "t-sub", x=M + bw + 30, y=by + bh / 2 - 22)

    slide("what", "What it is: one program, installed on a PC, a server, a NAS or a Pi. It checks things on a "
          "schedule, keeps history, and alerts with restraint.")
    y = title("wh", "WHAT IT IS", "A calm monitor for\neverything you run.", "everything you run.")
    p.apply([para("wh-body", SUB, M, y + 50, 36, 760, MUTED, line_height=1.35)], detail="brief")
    chips_row(p, "wh-chk", CHECKS, M, bottom(p, "wh-body") + 50, size=24, max_x=M + 780, dot_color=UP)
    screen(p, "wh-shot", "node", 1000, 150, 1100, height=760)

    slide("tour", "The dashboard: overall health, what needs attention, latency and response charts, groups, "
          "certificates. Widgets drag where you drop them.")
    y = title("to", "THE DASHBOARD", "Everything, at a glance.", "at a glance.")
    screen(p, "to-shot", "dashboard", M, y + 50, W - 2 * M + 200, height=H - y - 50 - 150)

    slide("map", "The network map draws each node under the one it depends on, and traces an outage's reach in "
          "red. Alerts for everything beneath a failed parent are suppressed, so you hear about the cause once.")
    y = title("ma", "THE NETWORK MAP", "One outage. One alert.", "One alert.")
    screen(p, "ma-shot", "map", M, y + 50, 1100, height=H - y - 50 - 150)
    cx = M + 1160
    pts = [("Dependencies", "The firewall on top; everything it would silence beneath."),
           ("Three layouts", "Styled to taste, edited in place."),
           ("Pin it", "Put the map on any dashboard or wallboard.")]
    ops = []
    for i, (t, d) in enumerate(pts):
        yy = y + 80 + i * 200
        ops += [dot(f"ma-dot{i}", cx, yy + 14, 20, [DOWN, WARN, UP][i]),
                text(f"ma-t{i}", t, cx + 44, yy, 40, TEXT, HEAD),
                para(f"ma-d{i}", d, cx + 44, yy + 58, 28, W - cx - M - 44, MUTED)]
    p.apply(ops, detail="brief")

    slide("features", "Six reasons people stay: the checks, the restraint, the map, the memory, the automation and "
          "the privacy.")
    y = title("fe", "WHY IT'S GOOD", "Robust enough to trust.\nSimple enough to enjoy.", "Simple enough to enjoy.",
              size=80)
    cw, ch, gap = (W - 2 * M - 2 * 32) / 3, 250, 32
    ops = []
    for i, (t, d) in enumerate(FEATURES):
        cx, cy = round(M + (i % 3) * (cw + gap)), y + 56 + (i // 3) * (ch + gap)
        ops += [rect(f"fe-card{i}", cx, cy, cw, ch, CARD, radius=14, stroke="#FFFFFF12", stroke_width=2),
                dot(f"fe-dot{i}", cx + 40, cy + 48, 18, [UP, WARN, DOWN, TEAL, GOLD, MUTED][i]),
                text(f"fe-t{i}", t, cx + 76, cy + 36, 38, TEXT, HEAD),
                para(f"fe-d{i}", d, cx + 40, cy + 104, 25, round(cw - 80), MUTED)]
    p.apply(ops, detail="brief")

    slide("anywhere", "Runs as a Windows service, a Linux/macOS program, a multi-arch Docker image or on a "
          "Raspberry Pi. SQLite by default; PostgreSQL or MySQL if you prefer.")
    y = title("an", "RUNS WHERE YOU DO", "Your hardware.\nYour data.", "Your data.")
    stats = [("5", "places it runs", "Windows, Linux, macOS, Docker, Raspberry Pi"),
             ("3", "database choices", "SQLite built in, or PostgreSQL / MySQL"),
             ("8", "ways to notify", ", ".join(NOTIFY[:-1]) + " and scripts"),
             ("0", "cloud accounts", "Nothing leaves your network")]
    cw = (W - 2 * M - 3 * 32) / 4
    ops = []
    for i, (n, label, d) in enumerate(stats):
        cx = round(M + i * (cw + 32))
        ops += [rect(f"an-rule{i}", cx, y + 80, cw, 6, [TEAL, UP, WARN, GOLD][i]),
                text(f"an-v{i}", n, cx, y + 120, 150, TEXT, HEAD),
                text(f"an-l{i}", label, cx, y + 310, 36, TEXT, SEMI),
                para(f"an-d{i}", d, cx, y + 368, 28, round(cw - 20), MUTED)]
    p.apply(ops, detail="brief")

    slide("beta", "What the beta is: 0.6.0 works and looks after your data. The interface and API can still change "
          "before 1.0, and that's exactly where testers come in.")
    y = title("be", "THE BETA", "It works. Now help\nus make it great.", "make it great.")
    asks = [("Find the bugs", "Anything that looks wrong, reads wrong or stops working.", DOWN),
            ("Spot the rough edges", "The screen that confused you, the setting you couldn't find.", WARN),
            ("Ask for features", "What would make GWatch the one you'd recommend?", UP)]
    cw = (W - 2 * M - 2 * 32) / 3
    ops = []
    for i, (t, d, c) in enumerate(asks):
        cx = round(M + i * (cw + 32))
        ops += [rect(f"be-card{i}", cx, y + 70, cw, 340, CARD, radius=14, stroke="#FFFFFF12", stroke_width=2),
                rect(f"be-bar{i}", cx, y + 70, 8, 340, c),
                text(f"be-t{i}", t, cx + 48, y + 120, 46, TEXT, HEAD),
                para(f"be-d{i}", d, cx + 48, y + 200, 30, round(cw - 96), MUTED)]
    p.apply(ops, detail="brief")
    p.apply([para("be-note", "Beta builds are signed, update themselves, and back up with one click, so trying "
                  "it is low-risk.", M, y + 460, 30, W - 2 * M, MUTED)], detail="brief")

    slide("join", "How to join: download from the latest GitHub release, run it on your network, and file issues for "
          "bugs, rough edges and feature ideas.")
    y = title("jo", "HOW TO JOIN", "Three steps. Ten minutes.", "Ten minutes.")
    cw = (W - 2 * M - 2 * 32) / 3
    ops = []
    for i, (t, d) in enumerate(JOIN_STEPS):
        cx = round(M + i * (cw + 32))
        ops += [text(f"jo-n{i}", f"0{i + 1}", cx, y + 80, 120, GOLD, HEAD),
                text(f"jo-t{i}", t, cx, y + 240, 42, TEXT, HEAD),
                para(f"jo-d{i}", d, cx, y + 310, 30, round(cw - 20), MUTED)]
    p.apply(ops, detail="brief")
    p.apply([text("jo-url1", "Download   " + REPO + "/releases", M, y + 520, 32, TEXT, MONO),
             text("jo-url2", "Feedback   " + ISSUES, M, y + 576, 32, TEXT, MONO)], detail="brief")

    slide("thanks", "Thank you. Every report makes 1.0 better.", master=None)
    p.apply([glow("th-glow", 300, -300, 2000, TEAL, "48"), *field("th-field", W, H, strength=0.8)], detail="brief")
    p.apply(mark("th-mark", (W - 300) // 2, 170, 300, "dark"), detail="brief")
    p.apply([headline("th-title", "Join the GWatch Beta.", 0, 540, 112, "GWatch Beta.", align="center", width=W),
             text("th-url", REPO, 0, 720, 44, TEAL, MONO)], detail="brief")
    center_x(p, "th-url", 0, W)

    save_and_export(p, "deck", "gwatch-beta-deck", [("pdf", {}), ("pptx", {}), ("html", {})],
                    checks=["bounds", "overlap", "contrast", "fonts", "deck"])
    sheet(p, OUT / "deck" / "contact-sheet.png", width=640, columns=3)
    p.export(str(OUT / "deck" / "gwatch-beta-deck.png"), page="title", scale=0.5)


def build_teaser():
    """Seven-second square motion teaser: MP4 and GIF."""
    W = H = 1080
    p = new(W, H)
    p.apply([glow("glow", -200, -200, 1500, TEAL, "40"), *field("field", W, H, strength=0.7)], detail="brief")
    p.apply(mark("mark", 340, 200, 400, "dark"), detail="brief")
    words = ["Ping.", "HTTPS.", "DNS.", "SNMP.", "Watched."]
    ops = [headline(f"w{i}", w, 0, 700, 120, "Watched." if i == 4 else None, align="center", width=W)
           for i, w in enumerate(words)]
    ops += [text("end-line", "Your home network, watched calmly.", 0, 610, 44, MUTED, SEMI),
            text("end-url", REPO, 0, 830, 34, TEAL, MONO),
            {"type": "timeline-set", "duration": 7200, "fps": 24}]
    p.apply(ops, detail="brief")
    lockup(p, "end-logo", 0, 380, 130)
    center_x(p, "end-logo", 0, W)
    for n in ("end-line", "end-url"):
        center_x(p, n, 0, W)
    beta_badge(p, "end-badge", 0, 712, 30)
    center_x(p, "end-badge", 0, W)
    ops = [{"type": "animate-preset", "target": "mark", "preset": "pop-in", "start": 0, "duration": 600},
           {"type": "keyframe", "target": "mark", "property": "opacity", "time": 4400, "value": 1},
           {"type": "keyframe", "target": "mark", "property": "opacity", "time": 4700, "value": 0}]
    for i in range(len(words)):
        t0 = 500 + i * 760
        hold = 1200 if i == len(words) - 1 else 620
        ops += [{"type": "keyframe", "target": f"w{i}", "property": "opacity", "time": 0, "value": 0,
                 "easing": "hold"},
                {"type": "keyframe", "target": f"w{i}", "property": "opacity", "time": t0, "value": 0},
                {"type": "keyframe", "target": f"w{i}", "property": "opacity", "time": t0 + 160, "value": 1},
                {"type": "keyframe", "target": f"w{i}", "property": "translate-y", "time": t0, "value": 40,
                 "easing": "ease-out-cubic"},
                {"type": "keyframe", "target": f"w{i}", "property": "translate-y", "time": t0 + 260, "value": 0},
                {"type": "keyframe", "target": f"w{i}", "property": "opacity", "time": t0 + hold, "value": 1},
                {"type": "keyframe", "target": f"w{i}", "property": "opacity", "time": t0 + hold + 140, "value": 0}]
    for n, t in (("end-logo", 4700), ("end-line", 5000), ("end-badge", 5250), ("end-url", 5450)):
        ops += [{"type": "keyframe", "target": n, "property": "opacity", "time": 0, "value": 0, "easing": "hold"},
                {"type": "animate-preset", "target": n, "preset": "slide-in-up", "start": t, "duration": 500,
                 "distance": 40, "fade": True}]
    p.apply(ops, detail="brief")
    folder = OUT / "motion"
    folder.mkdir(parents=True, exist_ok=True)
    p.save(str(folder / "teaser.vixl"))
    from vixl.timeline import contact_sheet, export_timeline, render_at

    export_timeline(p, str(folder / "teaser.mp4"), fps=24, overwrite=True)
    export_timeline(p, str(folder / "teaser.gif"), fps=12, scale=0.5, colors=96, overwrite=True)
    render_at(p, 6800).convert("RGB").save(folder / "teaser-frame.png")
    contact_sheet(p, count=12, columns=6, max_width=1800).convert("RGB").save(folder / "teaser-contact-sheet.png")
    print("  wrote motion/teaser.{vixl,mp4,gif} and contact sheet")


def build_overview():
    """One board showing the whole kit, framed from the exports above."""
    W, H = 2400, 1640
    p = new(W, H)
    o = "output"
    tiles = [  # path, x, y, w, h, label
        (f"{o}/social/og-card-1200x630.png", 80, 220, 900, 473, "Link card"),
        (f"{o}/social/github-preview-1280x640.png", 80, 750, 900, 450, "GitHub preview"),
        (f"{o}/social/email-header-1200x400.png", 80, 1260, 900, 300, "Email header"),
        (f"{o}/print/poster-tabloid.png", 1030, 220, 430, 659, "Tabloid poster, CMYK PDF"),
        (f"{o}/print/beta-flyer-letter.png", 1030, 940, 430, 556, "Beta flyer"),
        (f"{o}/social/story-1080x1920.png", 1510, 220, 380, 676, "Story"),
        (f"{o}/social/square-1080x1080.png", 1510, 960, 380, 380, "Square post"),
        (f"{o}/deck/gwatch-beta-deck.png", 1940, 220, 380, 214, "Beta deck"),
        (f"{o}/carousel/slide-1-cover.png", 1940, 500, 380, 475, "Carousel"),
        (f"{o}/motion/teaser-frame.png", 1940, 1040, 300, 300, "Motion teaser"),
    ]
    p.apply([*field("field", W, H, strength=0.5)], detail="brief")
    lockup(p, "logo", 80, 60, 84)
    p.apply([headline("head", "Beta launch kit, made with Vixl.", 520, 76, 64, "made with Vixl.")], detail="brief")
    ops = []
    for i, (path, x, y, w, h, label) in enumerate(tiles):
        ops += [rect(f"t{i}-base", x, y, w, h, CARD, radius=12),
                {"type": "frame", "name": f"t{i}", "path": path, "x": x, "y": y, "width": w, "height": h,
                 "fit": "fill"},
                {"type": "clip", "target": f"t{i}", "base": f"t{i}-base"}]
    p.apply(ops, detail="brief")
    p.apply({"type": "look", "targets": [f"t{i}-base" for i in range(len(tiles))], "look": "soft-shadow",
             "color": "#000000", "amount": 0.4}, detail="brief")
    for i, (path, x, y, w, h, label) in enumerate(tiles):
        chip(p, f"lab{i}", label, x + 10, y + h - 46, 18, TEXT, "#0F1114E0", pad=(12, 8))
    save_and_export(p, ".", "kit-overview", [("png", {"scale": 0.75})], checks=["bounds", "fonts"])


PIECES = {
    "og": build_og,
    "github": build_github,
    "square": build_square,
    "story": build_story,
    "banner": build_banner,
    "carousel": build_carousel,
    "flyer": build_flyer,
    "poster": build_poster,
    "deck": build_deck,
    "teaser": build_teaser,
    "overview": build_overview,
}


def main(argv):
    for name in argv or list(PIECES):
        print(f"[{name}]")
        PIECES[name]()


if __name__ == "__main__":
    main(sys.argv[1:])
