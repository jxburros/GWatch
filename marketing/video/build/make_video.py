#!/usr/bin/env python3
"""Build "GWatch in 60 seconds": twelve editable scene masters, a synthesised score, one MP4.

    python marketing/video/build/make_video.py            # scenes, audio, film-spec.json and the MP4
    python marketing/video/build/make_video.py --scenes   # only rebuild the scene masters

The film is assembled by Vixl's ``film-export`` workflow from the .vixl timelines.
"""
from __future__ import annotations

import json
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import scenes as S  # noqa: E402
from kit import FPS, H, VIDEO, W  # noqa: E402

OUTPUT = "gwatch-in-60-seconds.mp4"


def main():
    built = [make() for make in S.SCENES]
    for scene in built:
        scene.build()
        print("built", scene.name, flush=True)
    if "--scenes" in sys.argv:
        return
    import score

    starts, t = {}, 0
    for sc in built:
        starts[sc.name] = t
        t += sc.duration
    audio = VIDEO / "audio"
    audio.mkdir(exist_ok=True)
    score.write_all(audio, starts, t / 1000)
    spec = {"version": 1, "width": W, "height": H, "fps": FPS,
            "shots": [{"source": f"scenes/{sc.name}.vixl", "duration": sc.duration} for sc in built],
            "audio": [{"source": "audio/music.wav", "start": 0, "volume": 0.7},
                      {"source": "audio/effects.wav", "start": 0, "volume": 0.4}]}
    (VIDEO / "film-spec.json").write_text(json.dumps({"output": OUTPUT, "spec": spec}, indent=2) + "\n")
    out = VIDEO / OUTPUT
    if out.exists():
        out.unlink()
    subprocess.run(["vixl", "workflow", "film-export", "--request", str(VIDEO / "film-spec.json"),
                    "--workspace", str(VIDEO)], check=True)


if __name__ == "__main__":
    main()
