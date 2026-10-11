"""Turn the supplied GWatch raster logos into the vector geometry that build.py draws with Vixl.

Run once, from the repository root, when the source artwork changes:

    python -m pip install potracer scipy
    python brand/source/trace.py

Writes geometry.json next to this file. build.py reads only that JSON, so building the kit needs
neither potracer nor scipy.

- GWatch Agent: the deerstalker hat, brim and G are irregular hand-drawn curves, so they are traced
  (potrace, at 4x with sub-pixel edges) from the original. The eye is redrawn as true circles.
- GWatch: every part is a circle, line or rectangle, so it is rebuilt from parameters that
  were least-squares fitted to the original (the fit misses the source by about 1 px on average).

All coordinates are in the 1254 x 1254 pixel space of the supplied files.
"""

import json
from pathlib import Path

import numpy as np
import potrace
from PIL import Image
from scipy import ndimage

HERE = Path(__file__).resolve().parent
UP = 4  # trace at 4x for sub-pixel accurate edges

# Sampled from the flat colour areas of the originals.
AGENT_COLORS = {"navy": "#022252", "accent": "#207DFB", "white": "#FFFFFF"}
GWATCH_COLORS = {"navy": "#061A3A", "accent": "#D3A32E", "white": "#FFFFFF"}

# Eye circles fitted to the GWatch Agent original.
AGENT_EYE = {
    "iris": [622.5, 744.6, 174.0],
    "pupil": [622.5, 744.6, 98.4],
    "highlight": [666.0, 704.5, 29.0],
}

# Fitted GWatch geometry (see the module docstring).
GWATCH = {
    "ring": {"cx": 615.5, "cy": 594.7, "outer": 339.3, "inner": 257.2},
    # The G opening: ring removed between this ray (degrees, clockwise from 3 o'clock) and the bar.
    "opening_angle": -33.4,
    "bar": {"x": 781.4, "top": 578.0, "bottom": 652.6},
    "handle": {"from": [846.3, 823.2], "to": [986.0, 981.2], "radius": 56.0},
    "iris": [612.4, 600.0, 149.0],
    "pupil": [612.4, 600.0, 83.0],
    "highlight": [651.5, 568.0, 24.1],
}


def load(path):
    rgba = np.asarray(Image.open(path).convert("RGBA")).astype(float) / 255
    alpha = rgba[..., 3]
    lum = rgba[..., :3] @ [0.2126, 0.7152, 0.0722]
    return alpha, lum


def upsample(field):
    image = Image.fromarray((np.clip(field, 0, 1) * 255).astype(np.uint8))
    size = (image.width * UP, image.height * UP)
    return np.asarray(image.resize(size, Image.BICUBIC)).astype(float) / 255 >= 0.5


def trace(mask):
    """potrace a boolean mask into contours of absolute M/C/L/Z commands in source pixels."""
    paths = potrace.Bitmap(~mask).trace(turdsize=64, alphamax=1.0, opticurve=True, opttolerance=0.2)
    contours = []
    for curve in paths:
        def pt(p):
            return [round(p.x / UP, 2), round(p.y / UP, 2)]

        commands = [["M", *pt(curve.start_point)]]
        for segment in curve.segments:
            if segment.is_corner:
                commands.append(["L", *pt(segment.c)])
                commands.append(["L", *pt(segment.end_point)])
            else:
                commands.append(["C", *pt(segment.c1), *pt(segment.c2), *pt(segment.end_point)])
        contours.append(commands)
    return contours


def circle_mask(shape, cx, cy, r):
    yy, xx = np.mgrid[0:shape[0], 0:shape[1]]
    return (xx - cx) ** 2 + (yy - cy) ** 2 < r * r


def agent():
    alpha, lum = load(HERE / "gwatch-agent-original.webp")
    navy_lum, white_lum = 0.03, 1.0
    darkness = np.clip((white_lum - lum) / (white_lum - navy_lum), 0, 1)
    ix, iy, ir = AGENT_EYE["iris"]
    eye = circle_mask(alpha.shape, ix, iy, ir + 3)

    # Navy: hat, brim and G. The pupil is drawn as a circle, so the eye is left out.
    navy = alpha * darkness
    navy[eye] = 0
    # Eye white: everything light, extended 3 px under the navy so the shapes overlap without seams.
    light = alpha * (1 - darkness)
    light[eye] = 1
    solid = ndimage.binary_dilation(light > 0.5, iterations=3) & (alpha > 0.5)
    light = np.maximum(light, solid * 1.0)
    navy_mask = upsample(navy)
    # Keep only the large navy shapes (drops speckle).
    labels, count = ndimage.label(navy_mask)
    sizes = ndimage.sum(navy_mask, labels, range(1, count + 1))
    navy_mask = np.isin(labels, 1 + np.flatnonzero(sizes > 2000 * UP * UP))
    white_mask = upsample(light)
    labels, count = ndimage.label(white_mask)
    sizes = ndimage.sum(white_mask, labels, range(1, count + 1))
    white_mask = labels == 1 + int(np.argmax(sizes))
    return {
        "colors": AGENT_COLORS,
        "navy": trace(navy_mask),
        "white": trace(white_mask),
        **AGENT_EYE,
    }


def main():
    data = {"agent": agent(), "gwatch": {"colors": GWATCH_COLORS, **GWATCH}}
    (HERE / "geometry.json").write_text(json.dumps(data, separators=(",", ":")))
    for contour_set in ("navy", "white"):
        print(contour_set, len(data["agent"][contour_set]), "contours")


if __name__ == "__main__":
    main()
