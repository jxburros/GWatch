"""Build scenes and write contact sheets of sampled frames: python preview.py OUT_DIR [scene-prefix ...]"""
import sys
import time

from PIL import Image

import scenes as S
from vixl.timeline import render_at

OUT = sys.argv[1]
wanted = sys.argv[2:]
for make in S.SCENES:
    scene = make()
    if wanted and not any(scene.name.startswith(w) for w in wanted):
        continue
    t0 = time.time()
    p = scene.build()
    times = [round(scene.duration * f) for f in (0.08, 0.2, 0.33, 0.45, 0.58, 0.7, 0.85, 0.99)]
    sheet = Image.new("RGB", (4 * 480, 2 * 270))
    for i, t in enumerate(times):
        im = render_at(p, t).convert("RGB").resize((480, 270))
        sheet.paste(im, ((i % 4) * 480, (i // 4) * 270))
    sheet.save(f"{OUT}/{scene.name}.jpg", quality=88)
    print(scene.name, f"{time.time() - t0:.1f}s", times, flush=True)
