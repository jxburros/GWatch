"""The nine shots of "GWatch in 60 seconds": one night on a home network, in GWatch's own visual language.

The story runs on a clock. At 23:42 the gateway drops; GWatch sends one email, not eleven, and another
when it is back. Then the interface itself (dashboard, map, discovery), the checks, the automation, the
people it is for and what it never does, until 07:00, when everything is quietly up again.

Times are milliseconds. Facts on screen come from the README, CHANGELOG and docs/ at the version in
``VERSION`` (see ../README.md). The outage uses nodes from the interface's demo network.
"""
from __future__ import annotations

import math

from kit import (A_SCRIPT, BODY, CARD, DIM, DOWN, ELEV, GOLD, H, HEAD, LINE, LINE_STRONG, MAINT, MONO, MUTED,
                 NAVY, REPO, SCRIM, SEMI, TEAL, TEXT, UNKNOWN, UP, VERSION, W, WARN, ZERO, Scene, brackets,
                 ellipse, glide, glow, grid, group, hms, label, line, mark, panel, rail, rect, rich, screen,
                 status_dot, tag, text)

NODES = 8


def centered_label(name, value, y, size=18, color=DIM, tracking=None):
    """A tracked Kode Mono label centred on the frame (Kode Mono advances 0.6 em)."""
    tr = round(size * 0.28) if tracking is None else tracking
    width = len(value) * (size * 0.6 + tr) - tr
    return label(name, value, (W - width) / 2, y, size, color, tr)


def stamp(seconds):
    s = int(seconds) % 86400
    return f"{s // 3600:02d}:{s // 60 % 60:02d}:{s % 60:02d}"


# ----------------------------------------------------------------------------------------------- 1
DOT = (960, 470)
GW_DOT = (656 - 98 + 26, 236)  # where the gateway's status dot sits in shot 2: the cold open lands on it


def s01_night():
    sc = Scene("01-night", 4000)
    sc.add(grid())
    sc.keys("grid-lines", "opacity", [(0, 0), (2600, 0), (3800, 1)], "ease-in-out")
    sc.keys("grid-wash", "opacity", [(0, 0), (2600, 0), (3800, 1)], "ease-in-out")
    cx, cy = DOT
    sc.add(glow("halo-up", cx, cy, 260, UP, "60"), glow("halo-down", cx, cy, 300, DOWN, "70"),
           ellipse("dot", cx, cy, 26, UP))
    sc.keys("halo-up", "opacity", [(0, 0), (200, 0), (700, 1), (1100, 0.6), (1500, 0)], "ease-in-out")
    sc.keys("halo-down", "opacity", [(0, 0), (1500, 0), (1650, 1), (2200, 0.65), (2700, 1), (3200, 0.7)],
            "ease-in-out")
    sc.keys("dot", "opacity", [(0, 0), (200, 0), (500, 1)])
    sc.keys("dot", "fill", [(0, UP), (1500, UP), (1560, DOWN)], "linear")
    sc.add(centered_label("log", f"23:41:58   ALL {NODES} NODES UP", 530, 22, DIM))
    sc.fade("log", 400, dur=300)
    sc.add(centered_label("log2", "23:42:03   GATEWAY › PING   DOWN", 530, 22, DOWN))
    sc.keys("log", "opacity", [(1500, 1), (1520, 0)], "linear")
    sc.words("log2", 1520, "typewriter", "char", 380)
    sc.add(rich("say", "The Wi-Fi just dropped.", 0, 250, 108, W, TEXT, HEAD, "center"))
    sc.words("say", 2000, "fade-up", "word", 450, 110)
    sc.add(rich("ask", "Is it the router? The Pi-hole? The internet?", 0, 620, 40, W, MUTED, SEMI, "center"))
    sc.words("ask", 2700, "fade-up", "word", 380, 60)
    # Match cut: everything else leaves, and the dot travels to the gateway on the console.
    for n in ("say", "ask", "log2"):
        sc.keys(n, "opacity", [(3350, 1), (3700, 0)], "ease-in-cubic")
    gx, gy = GW_DOT
    for n in ("dot", "halo-down"):
        sc.keys(n, "translate-x", [(0, 0), (3350, 0), (4000, gx - cx)], "ease-in-out-cubic")
        sc.keys(n, "translate-y", [(0, 0), (3350, 0), (4000, gy - cy)], "ease-in-out-cubic")
        sc.keys(n, "scale", [(0, 1), (3350, 1), (4000, 14 / 26)], "ease-in-out-cubic")
    return sc


# ----------------------------------------------------------------------------------------------- 2
TREE = {"gw": ("Gateway", 656, 236, None),
        "sw": ("Office switch", 356, 386, "gw"), "ph": ("Pi-hole", 656, 386, "gw"), "px": ("Plex", 956, 386, "gw"),
        "nas": ("NAS", 230, 546, "sw"), "ha": ("Home Assistant", 450, 546, "sw"),
        "cam": ("Garage camera", 670, 546, "sw"), "bk": ("Backup server", 890, 546, "sw")}
NW, NH = 196, 52
# Story time against scene time: the clock holds while the console assembles, runs through the outage,
# then hurries to the recovery four minutes later. Breakpoints are (scene ms, story seconds).
STORY = [(0, hms(23, 42, 3)), (900, hms(23, 42, 3)), (3400, hms(23, 42, 40)), (6600, hms(23, 46, 0)),
         (7600, hms(23, 46, 5)), (13000, hms(23, 46, 50))]
AXIS = (150, 1838, hms(23, 41, 30), hms(23, 47, 30))  # x0, x1 and the story time they show


def story_at(t):
    for (t0, s0), (t1, s1) in zip(STORY, STORY[1:]):
        if t <= t1:
            return s0 + (s1 - s0) * (t - t0) / max(1, t1 - t0)
    return STORY[-1][1]


def scene_at(story):
    for (t0, s0), (t1, s1) in zip(STORY, STORY[1:]):
        if s1 > s0 and story <= s1:
            return t0 + (t1 - t0) * (story - s0) / (s1 - s0)
    return STORY[-1][0]


def axis_x(story):
    x0, x1, s0, s1 = AXIS
    return x0 + (x1 - x0) * (story - s0) / (s1 - s0)


T_SPREAD, T_RECOVER = 1100, scene_at(hms(23, 46, 2))
T_MAIL1, T_MAIL2 = scene_at(hms(23, 42, 12)), scene_at(hms(23, 46, 5))
T_SCRIM = 10000


def node(sc, key, label_text, cx, cy):
    n = f"n-{key}"
    x, y = cx - NW / 2, cy - NH / 2
    sc.add(glow(f"{n}-glow", cx, cy, NW * 1.5, DOWN, "40"),
           rect(f"{n}-bg", x, y, NW, NH, ELEV, LINE_STRONG, 1),
           rect(f"{n}-bar", x, y, 4, NH, UP),
           ellipse(f"{n}-dot", x + 26, cy, 12, UP),
           text(f"{n}-name", label_text, x + 42, cy - 13, 21, TEXT, SEMI),
           group(n, [f"{n}-glow", f"{n}-bg", f"{n}-bar", f"{n}-dot", f"{n}-name"]))
    sc.key(f"{n}-glow", "opacity", 0, 0)
    return n


def s02_outage():
    sc = Scene("02-outage", 13000)
    sc.add(grid())
    ops, rail_w = rail("rail")
    sc.add(ops)
    sc.slide("rail", 0, dx=-rail_w, dur=500)
    # Top bar: title, indicator line, the accent hairline and the clock.
    sc.add(text("top-title", "Network map", rail_w + 32, 10, 30, TEXT, HEAD),
           ellipse("top-dot", rail_w + 40, 58, 11, DOWN),
           text("top-ind", "Nodes down — 1   ·   Affected — 7", rail_w + 54, 47, 18, MUTED, MONO),
           rect("top-line", rail_w, 82, W - rail_w, 2, "#43C9C08C"),
           text("clock", stamp(STORY[0][1]), 1700, 20, 30, TEXT, MONO))
    for n in ("top-title", "top-dot", "top-ind", "clock"):
        sc.fade(n, 150, dur=300)
    sc.keys("top-line", "scale", [(0, 0.0), (100, 0.0), (700, 1)], "ease-out-expo")
    sc.texts("clock", [(t, stamp(story_at(t))) for t in range(0, 13001, 100)])
    sc.keys("top-dot", "fill", [(0, DOWN), (T_RECOVER, DOWN), (T_RECOVER + 200, UP)], "linear")
    sc.texts("top-ind", [(0, "Nodes down — 1   ·   Affected — 7"), (T_RECOVER + 100, f"All {NODES} nodes up")])

    # Dependencies, drawn like the interface's network map.
    sc.add(panel("dep", 96, 100, 1120, 590, f"Dependencies   ·   {NODES} nodes"))
    sc.fade("dep", 100, dur=350)
    sc.slide("dep", 100, dy=24, dur=500)
    for key, (name, cx, cy, parent) in TREE.items():
        if not parent:
            continue
        _, px, py, _ = TREE[parent]
        mid = (py + NH / 2 + cy - NH / 2) / 2
        ln = f"link-{key}"
        sc.add(line(ln, [(px, py + NH / 2), (px, mid), (cx, mid), (cx, cy - NH / 2)], LINE_STRONG, 2))
        sc.fade(ln, 350, dur=300)
        t_red = T_SPREAD + (0 if parent == "gw" else 300)
        sc.keys(ln, "stroke_color", [(0, LINE_STRONG), (t_red, LINE_STRONG), (t_red + 250, "#FF5C5CB0"),
                                     (T_RECOVER, "#FF5C5CB0"), (T_RECOVER + 400, LINE_STRONG)], "linear")
    for i, (key, (name, cx, cy, parent)) in enumerate(TREE.items()):
        n = node(sc, key, name, cx, cy)
        sc.fade(n, 250 + i * 60, dur=250)
        if key == "gw":
            for part, prop, a in ((f"{n}-bar", "fill", UP), (f"{n}-dot", "fill", UP),
                                  (f"{n}-bg", "stroke_color", LINE_STRONG)):
                sc.keys(part, prop, [(0, DOWN), (T_RECOVER, DOWN), (T_RECOVER + 300, a)], "linear")
            sc.keys(f"{n}-bg", "fill", [(0, "#2A1A1D"), (T_RECOVER, "#2A1A1D"), (T_RECOVER + 300, ELEV)], "linear")
            sc.keys(f"{n}-glow", "opacity", [(0, 0), (300, 0), (600, 1), (1400, 0.6), (2200, 1), (3000, 0.6),
                                             (3800, 1), (4600, 0.6), (5400, 1), (6200, 0.6), (T_RECOVER, 1),
                                             (T_RECOVER + 400, 0)], "ease-in-out")
            sc.keys("n-gw-dot", "opacity", [(0, 0), (500, 0), (520, 1)], "linear")
            # The cold open's dot arrives here before the node has drawn, then hands over to the node's own dot.
            sc.add(glow("land-glow", GW_DOT[0], GW_DOT[1], 150, DOWN, "70"), ellipse("land", *GW_DOT, 14, DOWN))
            for n in ("land", "land-glow"):
                sc.keys(n, "opacity", [(0, 1), (500, 1), (700, 0)], "linear")
        else:
            t_aff = T_SPREAD + (0 if parent == "gw" else 300) + 200
            for part in (f"{n}-bar", f"{n}-dot"):
                sc.keys(part, "fill", [(0, UP), (t_aff, UP), (t_aff + 200, UNKNOWN), (T_RECOVER + 200, UNKNOWN),
                                       (T_RECOVER + 500, UP)], "linear")
            sc.keys(f"{n}-name", "color", [(0, TEXT), (t_aff, TEXT), (t_aff + 200, MUTED), (T_RECOVER + 200, MUTED),
                                           (T_RECOVER + 500, TEXT)], "linear")
    sc.add(text("legend", "", 124, 640, 18, MUTED, MONO))
    sc.fade("legend", 500, dur=300)
    sc.texts("legend", [(0, f"Up {NODES}   Down 0   Affected 0"), (300, f"Up {NODES - 1}   Down 1   Affected 0"),
                        (T_SPREAD + 500, "Up 0   Down 1   Affected 7   ·   each node sits under the one it depends on"),
                        (T_RECOVER + 300, f"Up {NODES}   Down 0   Affected 0   ·   all clear")])

    # Overall health: the big tabular numerals, lit only when they mean something.
    sc.add(panel("health", 1248, 100, 640, 270, "Overall health"))
    sc.fade("health", 200, dur=350)
    sc.slide("health", 200, dy=24, dur=500)
    cols = [("up", "Up", UP, [(0, "0"), (T_RECOVER + 300, str(NODES))]),
            ("down", "Down", DOWN, [(0, "1"), (T_RECOVER + 300, "0")]),
            ("aff", "Affected", UNKNOWN, [(0, "0"), (T_SPREAD + 300, "7"), (T_RECOVER + 300, "0")])]
    for i, (key, name, colour, frames) in enumerate(cols):
        x = 1288 + i * 200
        lit = [(t, colour if v != "0" else ZERO) for t, v in frames]
        sc.add(glow(f"h-{key}-glow", x + 30, 225, 220, colour, "45"),
               text(f"h-{key}", frames[0][1], x, 176, 84, colour, MONO),
               rect(f"h-{key}-tick", x + 4, 285, 2, 16, colour),
               label(f"h-{key}-label", name, x, 310, 16))
        sc.texts(f"h-{key}", frames)
        sc.keys(f"h-{key}", "color", lit, "linear")
        sc.keys(f"h-{key}-tick", "fill", lit, "linear")
        sc.keys(f"h-{key}-glow", "opacity", [(0, 0), (400, 0)] + [(max(t, 700), 1 if v != "0" else 0) for t, v in frames],
                "linear")
        for n in (f"h-{key}", f"h-{key}-tick", f"h-{key}-label"):
            sc.keys(n, "opacity", [(0, 0), (400, 0), (700, 1)])
        sc.keys(f"h-{key}-glow", "scale", [(0, 0.9), (700, 1)])

    # Needs attention.
    sc.add(panel("att", 1248, 400, 640, 290, "Needs attention"))
    sc.fade("att", 300, dur=350)
    sc.slide("att", 300, dy=24, dur=500)
    rows = [("r1", DOWN, "Down", "Gateway › Ping", "No reply from 192.168.1.1 (4 of 4 lost)", 700),
            ("r2", UNKNOWN, "Held", "7 nodes behind it", "Affected by Gateway: no alerts of their own", T_SPREAD + 500)]
    for i, (r, colour, word, title, body, t) in enumerate(rows):
        y = 476 + i * 100
        sc.add(rect(f"{r}-bar", 1272, y, 3, 76, colour),
               tag(f"{r}-tag", word, 1292, y + 4, colour, 15, width=86),
               text(f"{r}-title", title, 1398, y, 24, TEXT, SEMI),
               text(f"{r}-body", body, 1398, y + 38, 18, MUTED, BODY),
               group(r, [f"{r}-bar", f"{r}-tag", f"{r}-title", f"{r}-body"]))
        sc.fade(r, t, T_RECOVER, dur=280)
        sc.slide(r, t, dx=40, dur=450)
    sc.add(rich("calm", "Nothing needs you.", 1248, 560, 28, 640, MUTED, SEMI, "center"))
    sc.fade("calm", T_RECOVER + 400, dur=400)

    # The notification timeline: eleven failing checks below the line, emails above it.
    sc.add(panel("notes", 96, 720, 1792, 330, "Alerts sent", menu=False))
    sc.fade("notes", 400, dur=350)
    sc.slide("notes", 400, dy=24, dur=500)
    ay = 930
    sc.add(rect("axis", AXIS[0], ay, AXIS[1] - AXIS[0], 1, LINE_STRONG))
    sc.fade("axis", 600, dur=300)
    for i, minute in enumerate(range(42, 48)):
        x = axis_x(hms(23, minute))
        sc.add(rect(f"tick{i}", x, ay - 6, 1, 12, LINE_STRONG), text(f"tickl{i}", f"23:{minute}", x - 28, ay + 66, 16,
                                                                     DIM, MONO))
        sc.fade(f"tick{i}", 600, dur=300)
        sc.fade(f"tickl{i}", 600, dur=300)
    for i in range(11):
        s = hms(23, 42, 3) + i * 3
        t = scene_at(s)
        x = axis_x(s)
        sc.add(rect(f"fail{i}", x - 2, ay + 10, 4, 22 + (i % 3) * 8, DOWN))
        sc.keys(f"fail{i}", "opacity", [(0, 0), (max(t, 900), 0), (max(t, 900) + 120, 1)], "linear")
    sc.add(label("fail-label", "11 failing checks", axis_x(hms(23, 42, 40)) + 18, ay + 18, 15, DOWN))
    sc.fade("fail-label", 3300, dur=300)
    sc.add(rect("playhead", axis_x(STORY[0][1]), 790, 2, 180, TEAL), glow("playglow", axis_x(STORY[0][1]), ay, 70,
                                                                             TEAL, "50"))
    for n in ("playhead", "playglow"):
        sc.fade(n, 800, dur=300)
        sc.keys(n, "translate-x", [(t, round(axis_x(s) - axis_x(STORY[0][1]), 1)) for t, s in STORY], "linear")

    def mail(name, story, colour, word, title, t, left=False):
        x = axis_x(story)
        cx = x - 486 if left else x + 16
        sc.add(rect(f"{name}-stem", x, 800, 2, ay - 800, colour),
               rect(f"{name}-pin", x - 6, ay - 6, 13, 13, colour),
               rect(f"{name}-card", cx, 788, 470, 92, ELEV, LINE_STRONG, 1),
               rect(f"{name}-cbar", cx + (467 if left else 0), 788, 3, 92, colour),
               label(f"{name}-kind", f"Email  ·  {word}", cx + 22, 802, 15, colour),
               text(f"{name}-title", title, cx + 22, 830, 26, TEXT, SEMI),
               group(name, [f"{name}-stem", f"{name}-pin", f"{name}-card", f"{name}-cbar", f"{name}-kind",
                            f"{name}-title"]))
        sc.fade(name, t, dur=250)
        sc.slide(name, t, dy=-16, dur=450)

    mail("mail1", hms(23, 42, 12), DOWN, "23:42", "Gateway is down", T_MAIL1)
    mail("mail2", hms(23, 46, 5), UP, "23:46", "Gateway recovered · down 4 min", T_MAIL2, left=True)
    sc.add(rich("tally", [("11", DOWN), (" failing checks.  ", None), ("1", TEXT), (" email.  ", None),
                          ("1", TEXT), (" all-clear.", None)], 1100, 734, 22, 760, MUTED, SEMI, "right"))
    sc.fade("tally", T_MAIL2 + 600, dur=400)

    # Everything gathers into one slow push, then steps back for the line.
    members = ["rail", "top-title", "top-dot", "top-ind", "top-line", "clock", "dep", "health", "att", "notes",
               "legend", "calm", "axis", "fail-label", "playhead", "playglow", "mail1", "mail2", "tally", "r1", "r2"]
    members += [f"link-{k}" for k in TREE if k != "gw"] + [f"n-{k}" for k in TREE]
    members += [f"h-{k}{s}" for k in ("up", "down", "aff") for s in ("", "-glow", "-tick", "-label")]
    members += [f"tick{i}" for i in range(6)] + [f"tickl{i}" for i in range(6)] + [f"fail{i}" for i in range(11)]
    sc.add(group("console", members))
    sc.keys("console", "scale", [(0, 1), (T_SCRIM, 1.02), (13000, 0.97)], "ease-in-out")
    sc.add(rect("scrim", 0, 0, W, H, SCRIM))
    sc.keys("scrim", "opacity", [(0, 0), (T_SCRIM, 0), (T_SCRIM + 500, 1)], "ease-in-out")
    sc.add(rich("line1", [("One outage. ", None), ("One alert.", TEAL)], 0, 380, 124, W, TEXT, HEAD, "center"))
    sc.words("line1", T_SCRIM + 300, "fade-up", "word", 480, 120)
    sc.add(rich("line2", "Dependency-aware, with cooldowns, silences and maintenance windows built in.", 0, 560, 38,
                W, MUTED, SEMI, "center"))
    sc.fade("line2", T_SCRIM + 1300, dur=450, rise=14)
    return sc


# ----------------------------------------------------------------------------------------------- 3
def s03_title():
    sc = Scene("03-title", 4000)
    sc.add(grid(wash=(0.5, 0.25)))
    box = (770, 100, 380, 380)
    sc.add(brackets("ret", 60, 60, 1800, 960, TEAL, 46, 3))
    glide(sc, "ret", (60, 60, 1800, 960), [(0, box, 900)])
    sc.keys("ret", "opacity", [(0, 0), (150, 1), (2600, 1), (3200, 0.35)], "ease-in-out")
    sc.add(mark("m", 810, 140, 300))
    sc.keys("m", "opacity", [(0, 0), (600, 0), (900, 1)])
    sc.keys("m", "scale", [(0, 0.85), (600, 0.85), (1100, 1)], "ease-out-back")
    for part in ("m-iris", "m-highlight"):  # the eye checks left, then right, then settles
        sc.keys(part, "translate-x", [(0, 0), (1300, 0), (1600, -10), (2000, 10), (2300, 0)], "ease-in-out-cubic")
    sc.add(rich("word", "GWatch", 0, 500, 150, W, TEXT, HEAD, "center"))
    sc.words("word", 900, "fade-up", "char", 380, 45)
    sc.add(rich("tag", [("Your home network, ", None), ("watched calmly.", TEAL)], 0, 700, 60, W, TEXT, SEMI,
                "center"))
    sc.words("tag", 1600, "fade-up", "word", 420, 90)
    sc.add(centered_label("sub", "Local  ·  Private  ·  Free to use", 815, 20))
    sc.fade("sub", 2400, dur=400)
    return sc


# ----------------------------------------------------------------------------------------------- 4
CHECKS = [("Gateway", "Ping", "Ping", UP, ["1.3 ms", "1.1 ms", "1.4 ms", "1.2 ms"]),
          ("Family website", "Homepage", "HTTP/S", UP, ["182 ms", "176 ms", "191 ms", "180 ms"]),
          ("Family website", "Certificate", "HTTPS certificate", WARN, ["9 days"]),
          ("Plex", "Port 32400", "TCP port", UP, ["open · 0.9 ms", "open · 1.0 ms"]),
          ("Pi-hole", "Resolves example.com", "DNS", UP, ["27 ms", "24 ms", "29 ms"]),
          ("Family website", "Says “Welcome”", "Keyword", UP, ["found"]),
          ("Weather API", "Status is ok", "JSON", UP, ["\"ok\""]),
          ("Backup server", "Nightly backup", "Custom script", UP, ["exit 0"]),
          ("NAS", "Disk /volume1", "Hardware health", UP, ["61 %", "61 %", "62 %"]),
          ("Office switch", "Port 3 in", "SNMP", UP, ["412 Mb/s", "388 Mb/s", "430 Mb/s", "405 Mb/s"])]


def spark(name, x, y, w, h, seed, colour):
    pts = []
    for i in range(30):
        u = i / 29
        v = 0.5 + 0.25 * math.sin(u * 8 + seed) + 0.15 * math.sin(u * 21 + seed * 2.1)
        pts.append((x + u * w, y + h * (1 - v)))
    return line(name, pts, colour, 2)


def s04_checks():
    sc = Scene("04-checks", 6000)
    sc.add(grid())
    sc.add(rich("big", [("Ten kinds of check.\n", None), ("One quiet list.", TEAL)], 96, 210, 88, 640, TEXT, HEAD,
                line_height=1.05))
    sc.words("big", 100, "fade-up", "word", 450, 90)
    sc.add(rich("sub", "Every reading has its own status, threshold, chart and history.", 96, 560, 32, 560, MUTED,
                BODY, line_height=1.35))
    sc.fade("sub", 1300, dur=400, rise=14)
    sc.add(rich("who", "Routers, NAS boxes, Pi-holes, websites, APIs, certificates, PCs and switches.", 96, 720, 26,
                560, DIM, BODY, line_height=1.4))
    sc.fade("who", 2200, dur=400, rise=14)
    px, py, pw = 760, 70, 1128
    sc.add(panel("list", px, py, pw, 950, "Checks   ·   10 types"))
    sc.fade("list", 0, dur=300)
    for i, (node_name, check, kind, colour, values) in enumerate(CHECKS):
        y = py + 70 + i * 87
        r = f"c{i}"
        word = "Up" if colour == UP else "Degraded"
        sc.add(rect(f"{r}-bar", px + 20, y, 3, 70, colour),
               tag(f"{r}-tag", word, px + 40, y + 6, colour, 14, width=112),
               text(f"{r}-name", f"{node_name} › {check}", px + 172, y + 2, 24, TEXT, SEMI),
               label(f"{r}-kind", kind, px + 172, y + 40, 14),
               text(f"{r}-val", values[0], px + 640, y + 14, 24, colour if colour != UP else TEXT, MONO),
               spark(f"{r}-spark", px + 860, y + 10, 230, 50, i * 1.9, colour),
               rect(f"{r}-sep", px + 20, y + 78, pw - 40, 1, LINE),
               group(r, [f"{r}-bar", f"{r}-tag", f"{r}-name", f"{r}-kind", f"{r}-val", f"{r}-spark", f"{r}-sep"]))
        t = 300 + i * 160
        sc.fade(r, t, dur=250)
        sc.slide(r, t, dy=30, dur=450)
        if len(values) > 1:
            sc.texts(f"{r}-val", [(0, values[0])] + [(t + 900 + k * 700 + i * 37, values[k % len(values)])
                                                     for k in range(1, 7)])
    return sc


# ----------------------------------------------------------------------------------------------- 5
# Boxes are where each part of the real interface sits once the screenshot spans the frame.
TOUR = [  # (start, screenshot, [(time, box, kicker, caption)])
    (0, "dashboard", [(300, (96, 187, 1797, 228), "Dashboard", "Status first: what's up, what's struggling, what needs you."),
                      (2200, (96, 427, 893, 466), "Dashboard", "Live charts from an hour to a year. Lay today over last week.")]),
    (4000, "map", [(4300, (120, 206, 1339, 477), "Network map", "Every node under the one it depends on, coloured by status."),
                   (6200, (1498, 288, 395, 494), "Network map", "Three layouts, styled to taste, pinned to any dashboard.")]),
    (8000, "discovery", [(8300, (428, 243, 1062, 652), "Discovery", "Scan your subnet. See names, reply times and open ports."),
                         (10200, (440, 300, 1040, 460), "Discovery", "Tick what you want. A template is suggested for each.")]),
]


def s05_tour():
    sc = Scene("05-tour", 12000)
    for i, (start, key, _) in enumerate(TOUR):
        sc.add(screen(f"shot{i}", key))
        end = TOUR[i + 1][0] if i + 1 < len(TOUR) else 12000
        sc.keys(f"shot{i}", "opacity", [(0, 0 if start else 1), (start, 0 if start else 1), (start + 400, 1),
                                        (end, 1), (end + 400, 1 if end >= 12000 else 0)], "ease-in-out")
        sc.keys(f"shot{i}", "scale", [(0, 1.06), (start, 1.06), (start + 900, 1.0)], "ease-out-cubic")
    big = (-40, -40, W + 80, H + 80)
    sc.add(brackets("ret", *big, TEAL, 44, 4))
    moves = [(t, box, 650) for _, _, stops in TOUR for t, box, *_ in stops]
    glide(sc, "ret", big, moves)
    # The caption: a square, opaque card in the corner, the way the interface draws a tooltip.
    sc.add(rect("cap-bg", 96, 918, 1000, 118, CARD, LINE_STRONG, 1), rect("cap-tick", 96, 918, 26, 2, TEAL),
           label("cap-kick", TOUR[0][2][0][2], 124, 940, 16, TEAL),
           text("cap-text", TOUR[0][2][0][3], 124, 976, 30, TEXT, SEMI),
           group("cap", ["cap-bg", "cap-tick", "cap-kick", "cap-text"]))
    sc.fade("cap", 500, dur=300)
    sc.slide("cap", 500, dy=20, dur=450)
    prev = None
    for _, _, stops in TOUR:
        for t, _, kick, words in stops:
            if prev is not None:
                sc.keys("cap-text", "opacity", [(t + 200, 1), (t + 320, 0), (t + 480, 1)], "linear")
                sc.keys("cap-kick", "opacity", [(t + 200, 1), (t + 320, 0), (t + 480, 1)], "linear")
            sc.texts("cap-text", [(t + 320 if prev else 0, words)])
            sc.texts("cap-kick", [(t + 320 if prev else 0, kick.upper())])
            prev = t
    sc.add(rect("demo-bg", 1500, 1000, 388, 40, "#0F1114E6"),
           label("demo", "Real interface · demo network", 1516, 1010, 14, DIM))
    sc.fade("demo", 600, dur=300)
    sc.fade("demo-bg", 600, dur=300)
    return sc


# ----------------------------------------------------------------------------------------------- 6
EVENTS = [("23:42:04", "Trigger", A_SCRIPT, "Plex goes down", "sh   docker restart plex", "Ok", "1.2 s"),
          ("23:43:10", "Rule", WARN, "Two of three DNS servers are down", "notify   ntfy  +  Pushover", "Sent", ""),
          ("06:58:12", "Endpoint", MAINT, "The router says it rebooted", "POST /hook/router-rebooted   →   run Gateway's checks",
           "Ok", "")]
ACTIONS = ["Email", "Webhooks", "Slack", "Teams", "ntfy", "Pushover", "git", "Scripts"]


def s06_automate():
    sc = Scene("06-automate", 6000)
    sc.add(grid(wash=(0.85, -0.1)))
    sc.add(rich("big", [("It doesn't just watch. ", None), ("It acts.", TEAL)], 96, 92, 84, 1700, TEXT, HEAD))
    sc.words("big", 100, "fade-up", "word", 450, 90)
    px, py, pw = 96, 240, 1728
    sc.add(panel("events", px, py, pw, 540, "Events   ·   triggers, rules and endpoints"))
    sc.fade("events", 200, dur=300)
    for i, (when, kind, colour, what, action, result, took) in enumerate(EVENTS):
        y = py + 84 + i * 150
        e = f"e{i}"
        sc.add(text(f"{e}-time", when, px + 28, y + 4, 22, DIM, MONO),
               tag(f"{e}-kind", kind, px + 200, y, colour, 15, width=140),
               text(f"{e}-what", what, px + 380, y - 2, 30, TEXT, SEMI),
               text(f"{e}-act", f"→  {action}", px + 380, y + 46, 22, MUTED, MONO),
               tag(f"{e}-res", result, px + pw - 230, y, UP, 15, width=90),
               text(f"{e}-took", took, px + pw - 120, y + 4, 20, DIM, MONO),
               rect(f"{e}-sep", px + 24, y + 112, pw - 48, 1, LINE),
               group(e, [f"{e}-time", f"{e}-kind", f"{e}-what", f"{e}-act", f"{e}-res", f"{e}-took", f"{e}-sep"]))
        t = 600 + i * 1000
        sc.fade(e, t, dur=250)
        sc.slide(e, t, dx=-30, dur=450)
        sc.words(f"{e}-act", t + 250, "typewriter", "char", 450)
        sc.keys(f"{e}-res", "opacity", [(0, 0), (t + 750, 0), (t + 800, 1)], "linear")
        sc.keys(f"{e}-took", "opacity", [(0, 0), (t + 750, 0), (t + 800, 1)], "linear")
    sc.add(label("can", "It can", 96, 850, 18))
    sc.fade("can", 3300, dur=300)
    x = 220
    for i, a in enumerate(ACTIONS):
        w = round(len(a) * 17 * 0.72 + 40)
        sc.add(tag(f"a{i}", a, x, 840, TEXT, 17, width=w))
        sc.fade(f"a{i}", 3400 + i * 110, dur=200)
        sc.slide(f"a{i}", 3400 + i * 110, dy=14, dur=350)
        x += w + 12
    sc.add(rich("sub", "When a check changes, when a rule across nodes is met, or when another system calls in.", 96,
                930, 32, 1700, MUTED, BODY))
    sc.fade("sub", 4400, dur=400, rise=12)
    return sc


# ----------------------------------------------------------------------------------------------- 7
FOR = [("For the homelab.", "Servers · NAS · Plex · Pi-hole"),
       ("For the smart home.", "Home Assistant · cameras · hubs"),
       ("For the family website.", "Uptime · keywords · certificate expiry"),
       ("For the small office.", "Switches · access points · SNMP · reports"),
       ("For the screen on the wall.", "Wallboards for a spare monitor or tablet")]


def s07_for():
    sc = Scene("07-for", 6000)
    sc.add(screen("wall", "wall", 0, (H - W * 1311 / 2400) / 2))
    sc.keys("wall", "scale", [(0, 1.0), (6000, 1.06)], "linear")
    sc.add({"type": "gradient", "name": "shade", "direction": "horizontal", "x": 0, "y": 0, "width": W, "height": H,
            "stops": [{"offset": 0, "color": "#0F1114F7"}, {"offset": 0.55, "color": "#0F1114E0"},
                      {"offset": 1, "color": "#0F111470"}]})
    sc.keys("shade", "opacity", [(0, 1), (4800, 1), (5500, 0)], "ease-in-out")
    for i, (big, small) in enumerate(FOR):
        t = 250 + i * 900
        last = i == len(FOR) - 1
        sc.add(text(f"for{i}", big, 120, 420, 104, TEXT, HEAD), label(f"forsub{i}", small, 124, 570, 20, MUTED))
        sc.words(f"for{i}", t, "fade-up", "word", 380, 70)
        sc.fade(f"forsub{i}", t + 250, dur=250)
        out = (4700, 5000) if last else (t + 760, t + 900)
        for n in (f"for{i}", f"forsub{i}"):
            sc.keys(n, "opacity", [(out[0], 1), (out[1], 0)], "linear")
    return sc


# ----------------------------------------------------------------------------------------------- 8
ABOUT = [("Cloud account", "None. Nothing to sign up for."),
         ("Phones home", "Never. It doesn't expose itself to the internet."),
         ("Updates", "Signed, and verified before they install."),
         ("Backups", "One click, password-encrypted, restorable anywhere."),
         ("Accessibility", "Keyboard and screen reader; a table for every chart."),
         ("Runs on", "Windows · Linux · macOS · Docker · Raspberry Pi"),
         ("Database", "SQLite built in, or your PostgreSQL or MySQL."),
         ("Price", "Free to use.")]


def s08_home():
    sc = Scene("08-home", 5000)
    sc.add(grid(wash=(0.1, 0.9)))
    sc.add(rich("big", "Stays home.", 96, 330, 112, 700, TEXT, HEAD))
    sc.words("big", 100, "fade-up", "word", 450, 110)
    sc.add(rich("sub", [("Your network. Your data. ", None), ("Your hardware.", TEAL)], 96, 470, 36, 720, MUTED,
                SEMI, line_height=1.3))
    sc.fade("sub", 700, dur=400, rise=12)
    px, py, pw = 840, 120, 1048
    sc.add(panel("about", px, py, pw, 840, "About"))
    sc.fade("about", 0, dur=300)
    for i, (k, v) in enumerate(ABOUT):
        y = py + 80 + i * 94
        r = f"a{i}"
        sc.add(label(f"{r}-k", k, px + 28, y + 18, 16),
               text(f"{r}-v", v, px + 300, y + 10, 26, TEXT, SEMI),
               *status_dot(f"{r}-dot", px + pw - 40, y + 26, UP, 10, 5),
               rect(f"{r}-sep", px + 24, y + 70, pw - 48, 1, LINE),
               group(r, [f"{r}-k", f"{r}-v", f"{r}-dot-glow", f"{r}-dot", f"{r}-sep"]))
        t = 400 + i * 330
        sc.fade(r, t, dur=250)
        sc.slide(r, t, dx=30, dur=400)
    return sc


# ----------------------------------------------------------------------------------------------- 9
def s09_end():
    sc = Scene("09-end", 4000)
    sc.add(grid(wash=(0.5, 0.3)))
    sc.add(mark("m", 600, 236, 200))
    sc.add(text("word", "GWatch", 830, 250, 150, TEXT, HEAD))
    sc.add(group("logo", ["m", "word"]))
    sc.keys("logo", "opacity", [(0, 0), (100, 0), (500, 1)])
    sc.keys("logo", "scale", [(0, 0.94), (100, 0.94), (700, 1)], "ease-out-cubic")
    sc.add(rich("line", [("Your home network, ", None), ("watched calmly.", TEAL)], 0, 520, 76, W, TEXT, HEAD,
                "center"))
    sc.words("line", 400, "fade-up", "word", 450, 90)
    badge = f"Free to use  ·  Beta {VERSION}"
    bw = round(len(badge) * 20 * 0.72 + 48)
    sc.add(rect("badge-bg", (W - bw) / 2, 652, bw, 44, GOLD),
           rich("badge", badge.upper(), (W - bw) / 2, 662, 20, bw, NAVY, MONO, "center", 1.2),
           group("badge-g", ["badge-bg", "badge"]))
    sc.fade("badge-g", 1100, dur=350)
    sc.add(rich("url", REPO, 0, 740, 44, W, TEAL, MONO, "center"))
    sc.fade("url", 1400, dur=400, rise=14)
    # Bookend: morning, and the dot from the cold open is green again.
    sc.add(*status_dot("dot", 112, 1006, UP, 14, 7), label("log", f"07:00:00   All {NODES} nodes up", 134, 996, 18))
    sc.fade("dot", 1700, dur=300)
    sc.breathe("dot-glow", 1700, 4000, 1600, 0.4)
    sc.fade("log", 1700, dur=300)
    made = "Made with Vixl · every frame is an editable document"
    sc.add(label("made", made, W - 96 - len(made) * (14 * 0.6 + 4), 1000, 14, ZERO, 4))
    sc.fade("made", 2000, dur=400)
    return sc


SCENES = [s01_night, s02_outage, s03_title, s04_checks, s05_tour, s06_automate, s07_for, s08_home, s09_end]
