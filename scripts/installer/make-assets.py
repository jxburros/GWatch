#!/usr/bin/env python3
"""Regenerates the installers' wizard artwork and the agent's icon.

    python3 scripts/installer/make-assets.py

Needs Python 3 and Pillow (pip install pillow). Everything it writes lands in
scripts/installer/assets/ and is committed, so this only needs running when a
master logo or the look below changes. It is deterministic: run twice with the
same Pillow, it writes the same bytes.

What it makes, from the two master logos in assets/:

  logo-master.png        (GWatch)  -> wizard-large.bmp, wizard-large-2x.bmp
  agent-logo-master.png  (Agent)   -> agent-wizard-large.bmp, agent-wizard-large-2x.bmp
                                      agent-wizard-small.bmp, agent-wizard-small-2x.bmp
                                      gwatch-agent.ico

GWatch's own small badge (wizard-small*.bmp) and its icon (gwatch.ico) predate
this script and are left alone; see README.md.

Both installers are light-only (issue #66), so every panel is drawn in the
application's LIGHT theme tokens from web/app.css. The large panel is the
app's skin in miniature: the --bg field with a hairline grid and a faint
accent wash, the logo on a white card, the teal accent rule, a sparkline, and
three status dots in the light theme's up/warn/down colours.
"""

import io
import os
import struct

from PIL import Image, ImageDraw, ImageFilter

HERE = os.path.dirname(os.path.abspath(__file__))
ASSETS = os.path.join(HERE, "assets")

# Light theme tokens, web/app.css :root[data-theme="light"]. The accent is the
# same teal in both themes.
BG = (0xF1, 0xF2, 0xF4)       # --bg
CARD = (0xFF, 0xFF, 0xFF)     # --card
LINE = (0xD5, 0xD8, 0xDD)     # --line
ACCENT = (0x43, 0xC9, 0xC0)   # --accent
ACCENT_TEXT = (0x28, 0x79, 0x73)  # --accent-hover in light (accent 60% + black)
UP = (0x10, 0x7F, 0x37)       # --up
WARN = (0x9F, 0x5F, 0x00)     # --warn
DOWN = (0xD4, 0x25, 0x25)     # --down
GRID_ALPHA = 0.045            # --grid is 0.035; a touch firmer so a bitmap keeps it

# Layout is written in the 2x panel's pixels (328x628) and drawn at SS times
# that, then scaled down to both sizes Inno asks for, so every edge is
# antialiased the same way at 1x and 2x.
LARGE_2X = (328, 628)
SMALL_2X = (110, 116)
SS = 2


def load_logo(name):
    """The master logo, cropped to what it actually draws."""
    im = Image.open(os.path.join(ASSETS, name)).convert("RGBA")
    return im.crop(im.getchannel("A").getbbox())


def fit(logo, box_w, box_h):
    """logo scaled to fit a box_w x box_h box, aspect kept."""
    scale = min(box_w / logo.width, box_h / logo.height)
    size = (max(1, round(logo.width * scale)), max(1, round(logo.height * scale)))
    return logo.resize(size, Image.LANCZOS)


def paste_centered(dst, logo, box):
    x0, y0, x1, y1 = box
    img = fit(logo, x1 - x0, y1 - y0)
    dst.alpha_composite(img, (x0 + (x1 - x0 - img.width) // 2, y0 + (y1 - y0 - img.height) // 2))


def over(dst, layer):
    dst.alpha_composite(layer)


def rgba(rgb, a):
    return rgb + (round(255 * a),)


def large_panel(logo):
    """The welcome/finish panel at SS x 2x, as RGBA."""
    k = SS  # one 2x-panel pixel, in drawing pixels
    W, H = LARGE_2X[0] * k, LARGE_2X[1] * k
    im = Image.new("RGBA", (W, H), rgba(BG, 1))

    # Accent wash from the top left, as --wash does in the app.
    wash = Image.new("L", (W, H), 0)
    cx, cy, r = int(W * 0.12), int(-H * 0.12), int(H * 0.75)
    ImageDraw.Draw(wash).ellipse((cx - r, cy - r, cx + r, cy + r), fill=round(255 * 0.10))
    wash = wash.filter(ImageFilter.GaussianBlur(H * 0.12))
    tint = Image.new("RGBA", (W, H), ACCENT + (0,))
    tint.putalpha(wash)
    over(im, tint)

    # Hairline grid, 16px at 1x.
    grid = Image.new("RGBA", (W, H), (0, 0, 0, 0))
    g = ImageDraw.Draw(grid)
    step = 32 * k
    for x in range(step, W, step):
        g.rectangle((x, 0, x + k - 1, H), fill=(0, 0, 0, round(255 * GRID_ALPHA)))
    for y in range(step, H, step):
        g.rectangle((0, y, W, y + k - 1), fill=(0, 0, 0, round(255 * GRID_ALPHA)))
    over(im, grid)

    # The card: white, a hairline border, and the light theme's soft shadow.
    card = (40 * k, 68 * k, 288 * k, 316 * k)
    shadow = Image.new("RGBA", (W, H), (0, 0, 0, 0))
    ImageDraw.Draw(shadow).rectangle(
        (card[0], card[1] + 10 * k, card[2], card[3] + 10 * k), fill=(20, 30, 50, round(255 * 0.14)))
    over(im, shadow.filter(ImageFilter.GaussianBlur(15 * k)))
    d = ImageDraw.Draw(im)
    d.rectangle(card, fill=rgba(CARD, 1), outline=rgba(LINE, 1), width=2 * k)
    pad = 30 * k
    paste_centered(im, logo, (card[0] + pad, card[1] + pad, card[2] - pad, card[3] - pad))

    # The accent rule the app draws under its top bar.
    d = ImageDraw.Draw(im)
    d.rectangle((72 * k, 356 * k, 255 * k, 358 * k - 1), fill=rgba(ACCENT, 1))

    # A sparkline over its baseline, with a faint fill under it.
    pts = [(40, 498), (62, 487), (84, 492), (108, 477), (130, 481), (151, 472),
           (174, 485), (197, 480), (220, 488), (244, 475), (264, 481), (288, 468)]
    pts = [(x * k, y * k) for x, y in pts]
    base = 512 * k
    fill = Image.new("RGBA", (W, H), (0, 0, 0, 0))
    ImageDraw.Draw(fill).polygon(pts + [(pts[-1][0], base), (pts[0][0], base)], fill=rgba(ACCENT, 0.12))
    over(im, fill)
    d = ImageDraw.Draw(im)
    d.rectangle((38 * k, 464 * k, 290 * k, 466 * k - 1), fill=rgba(LINE, 1))
    d.line(pts, fill=rgba(ACCENT_TEXT, 1), width=2 * k, joint="curve")

    # Status dots: up, warn, down, each with a soft halo.
    halo = Image.new("RGBA", (W, H), (0, 0, 0, 0))
    hd = ImageDraw.Draw(halo)
    for x, c in ((63, UP), (115, WARN), (167, DOWN)):
        cx, cy = x * k, 563 * k
        hd.ellipse((cx - 14 * k, cy - 14 * k, cx + 14 * k, cy + 14 * k), fill=rgba(c, 0.14))
    over(im, halo)
    d = ImageDraw.Draw(im)
    for x, c in ((63, UP), (115, WARN), (167, DOWN)):
        cx, cy = x * k, 563 * k
        d.ellipse((cx - 8 * k, cy - 8 * k, cx + 8 * k, cy + 8 * k), fill=rgba(c, 1))
    return im


def small_badge(logo):
    """The inner pages' header badge at SS x 2x: the mark on the header's white."""
    k = SS
    W, H = SMALL_2X[0] * k, SMALL_2X[1] * k
    im = Image.new("RGBA", (W, H), rgba(CARD, 1))
    paste_centered(im, logo, (11 * k, 8 * k, 99 * k, 108 * k))
    return im


def save_bmp(im, size, name):
    """A 24-bit BMP, which is what Inno's WizardImageFile wants."""
    out = im.resize(size, Image.LANCZOS).convert("RGB")
    out.save(os.path.join(ASSETS, name), format="BMP")


def save_pair(im, size_2x, stem):
    save_bmp(im, size_2x, stem + "-2x.bmp")
    save_bmp(im, (size_2x[0] // 2, size_2x[1] // 2), stem + ".bmp")


def dib_entry(img):
    """One .ico image as a 32-bit BGRA DIB with its (all-opaque) AND mask."""
    w, h = img.size
    header = struct.pack("<IiiHHIIiiII", 40, w, h * 2, 1, 32, 0, 0, 0, 0, 0, 0)
    px = img.tobytes("raw", "BGRA")
    rows = [px[y * w * 4:(y + 1) * w * 4] for y in range(h)]
    xor = b"".join(reversed(rows))  # a DIB is stored bottom-up
    mask_row = ((w + 31) // 32) * 4
    and_mask = b"\x00" * (mask_row * h)  # alpha does the work; the mask is all "draw"
    return header + xor + and_mask


def save_ico(logo, name):
    """Multi-size icon, laid out like gwatch.ico: 16-128 as 32-bit DIBs, 256 as PNG."""
    sizes = (16, 24, 32, 48, 64, 128, 256)
    images = []
    for n in sizes:
        canvas = Image.new("RGBA", (n, n), (0, 0, 0, 0))
        # A hair of margin at the larger sizes; at 16 and 24 every pixel counts.
        m = 0 if n <= 24 else max(1, n // 32)
        paste_centered(canvas, logo, (m, m, n - m, n - m))
        if n == 256:
            buf = io.BytesIO()
            canvas.save(buf, format="PNG", optimize=True)
            images.append(buf.getvalue())
        else:
            images.append(dib_entry(canvas))
    head = struct.pack("<HHH", 0, 1, len(sizes))
    offset = 6 + 16 * len(sizes)
    table = b""
    for n, data in zip(sizes, images):
        wh = 0 if n == 256 else n
        table += struct.pack("<BBBBHHII", wh, wh, 0, 0, 1, 32, len(data), offset)
        offset += len(data)
    with open(os.path.join(ASSETS, name), "wb") as f:
        f.write(head + table + b"".join(images))


def main():
    gwatch = load_logo("logo-master.png")
    agent = load_logo("agent-logo-master.png")

    save_pair(large_panel(gwatch), LARGE_2X, "wizard-large")
    save_pair(large_panel(agent), LARGE_2X, "agent-wizard-large")
    save_pair(small_badge(agent), SMALL_2X, "agent-wizard-small")
    save_ico(agent, "gwatch-agent.ico")


if __name__ == "__main__":
    main()
