"""The twelve shots of "GWatch in 60 seconds". Each function returns a ``Scene``; times are milliseconds.

The score runs at 120 BPM, so a beat is 500 ms and most entrances land on one. Facts on screen come
from the README, CHANGELOG and docs/ at the version in ``VERSION`` (see ../README.md).
"""
from __future__ import annotations

import math

from kit import (BG, BODY, CARD, DOWN, ELEV, GOLD, GREY, HEAD, LINE, MONO, MUTED, NAVY, REPO, SEMI, TEAL, TEXT,
                 UP, VERSION, WARN, W, Scene, backdrop, centered, chip, ellipse, glow, group, headline, kicker,
                 line, mark, rect, rich, shadow, text, window)


def exit_left(sc, names, t, dur=550):
    """Slide and fade a set of layers out to the left, so the next cut lands on a clean frame."""
    for n in names:
        sc.keys(n, "translate-x", [(0, 0), (t, 0), (t + dur, -120)], "ease-in-cubic")


# ----------------------------------------------------------------------------------------------- 1
HOOK_DEVICES = ["Router", "Pi-hole", "NAS", "Plex", "Home Assistant", "The internet"]


def s01_hook():
    sc = Scene("01-hook", 5000)
    sc.add(backdrop(glow_at=(1600, 300), strength=0.35))
    sc.add(text("time", "11:42 PM", 160, 200, 34, GOLD, MONO))
    sc.fade("time", 100, dur=300)
    sc.add(rich("l1", "The Wi-Fi is down.", 160, 260, 120, 1100, TEXT, HEAD))
    sc.words("l1", 300, "fade-up", "word", 480, 100)
    sc.add(rich("l2", "Is it the router? The Pi-hole? The internet?", 160, 420, 50, 1000, MUTED, SEMI,
                line_height=1.25))
    sc.words("l2", 1300, "fade-up", "word", 420, 70)
    sc.add(rich("l3", [("Stop guessing. ", None), ("Know.", TEAL)], 160, 620, 110, 1100, TEXT, HEAD))
    sc.words("l3", 2900, "pop", "word", 480, 220)
    # Devices on the right, each a question until GWatch answers.
    x0, y0 = 1330, 230
    for i, name in enumerate(HOOK_DEVICES):
        y = y0 + i * 105
        row = f"dev{i}"
        sc.add(rect(f"{row}-bg", x0, y, 440, 80, CARD, 16, LINE),
               ellipse(f"{row}-dot", x0 + 44, y + 40, 22, 22, GREY),
               text(f"{row}-name", name, x0 + 80, y + 22, 30, TEXT, SEMI),
               text(f"{row}-q", "?", x0 + 390, y + 18, 36, MUTED, MONO),
               group(row, [f"{row}-bg", f"{row}-dot", f"{row}-name", f"{row}-q"]))
        sc.fade(row, 500 + i * 160, dur=300)
        sc.slide(row, 500 + i * 160, dx=60, dur=500)
        # The dots flicker while nobody knows, then settle on their real state.
        frames = [(0, GREY)]
        for k in range(4):
            frames += [(1300 + k * 400 + i * 40, DOWN if (i + k) % 3 == 0 else GREY),
                       (1500 + k * 400 + i * 40, GREY)]
        final = DOWN if i == 0 else (UP if i != 5 else GREY)
        frames += [(2900 + i * 80, GREY), (3100 + i * 80, final)]
        sc.keys(f"{row}-dot", "fill", frames, "linear")
        sc.key(f"{row}-q", "text", 0, "?")
        sc.key(f"{row}-q", "text", 2900 + i * 80, {0: "DOWN", 5: "—"}.get(i, "UP"))
        sc.keys(f"{row}-q", "color", [(2900 + i * 80, MUTED), (3100 + i * 80, final)], "linear")
        sc.keys(f"{row}-q", "translate-x", [(2900 + i * 80, 0), (2901 + i * 80, {0: -48, 5: 0}.get(i, 0))], "hold")
    sc.keys("l1", "opacity", [(2700, 1), (3200, 0.3)], "ease-in-out")
    sc.keys("l2", "opacity", [(2700, 1), (3200, 0.3)], "ease-in-out")
    names = ["time", "l1", "l2", "l3"] + [f"dev{i}" for i in range(6)]
    exit_left(sc, names, 4450)
    for n in names:
        sc.keys(n, "opacity", [(4450, 1 if n in ("l3", "time") or n.startswith("dev") else 0.3), (4950, 0)],
                "ease-in-cubic")
    return sc


# ----------------------------------------------------------------------------------------------- 2
def s02_logo():
    sc = Scene("02-logo", 4000)
    sc.add(backdrop(glow_at=(960, 380), strength=0.45))
    sc.add(glow("pulse", 960, 330, 1000, TEAL, "50"))
    sc.keys("pulse", "scale", [(0, 0.4), (200, 0.4), (1100, 1.05)], "ease-out-cubic")
    sc.keys("pulse", "opacity", [(0, 0), (200, 0), (700, 1)])
    sc.add(mark("m", 825, 150, 270))
    sc.pop("m", 0, 650, 0.3)
    sc.keys("m", "rotation", [(0, -25), (650, 0)], "ease-out-back")
    # The eye looks around once, then settles: the iris, pupil and glint move together.
    for part in ("m-c1", "m-c2", "m-c3"):
        sc.keys(part, "translate-x", [(0, 0), (900, 0), (1250, -9), (1650, 9), (1950, 0)], "ease-in-out-cubic")
    sc.add(centered("word", "GWatch", 440, 160))
    sc.words("word", 600, "pop", "char", 400, 60)
    sc.add(centered("tag", [("Your home network, ", None), ("watched calmly.", TEAL)], 650, 60, TEXT, HEAD))
    sc.words("tag", 1400, "fade-up", "word", 480, 90)
    sc.add(centered("sub", "Local  ·  Private  ·  Free to use", 770, 32, MUTED, MONO))
    sc.fade("sub", 2300, dur=450, rise=14)
    return sc


# ----------------------------------------------------------------------------------------------- 3
CHECKS = [("Ping", "192.168.1.1  ·  1.3 ms", UP), ("HTTP/S", "family.example  ·  200", UP),
          ("Certificate", "expires in 9 days", WARN), ("TCP port", "plex  ·  :32400", UP),
          ("DNS", "pi-hole resolves", UP), ("Keyword", "page says “Welcome”", UP),
          ("JSON", "$.status = \"ok\"", UP), ("Custom script", "check-backup.sh", UP),
          ("Hardware", "CPU 23%  ·  disk 61%", UP), ("SNMP", "switch port 3  ·  412 Mb/s", UP)]


def spark(name, x, y, w, h, seed, color):
    pts = []
    for i in range(25):
        u = i / 24
        v = 0.5 + 0.28 * math.sin(u * 9 + seed) + 0.14 * math.sin(u * 23 + seed * 2.3)
        pts.append((x + u * w, y + h * (1 - v)))
    return line(name, pts, color, 3)


def s03_checks():
    sc = Scene("03-checks", 5000)
    sc.add(backdrop(glow_at=(1600, 300)))
    kicker(sc, "01", "CHECKS EVERYTHING")
    headline(sc, [("It watches ", None), ("everything you run.", TEAL)], t=100)
    cw, ch, gap = 318, 220, 22
    for i, (name, detail, status) in enumerate(CHECKS):
        x = 120 + (i % 5) * (cw + gap)
        y = 300 + (i // 5) * (ch + gap)
        c = f"chk{i}"
        sc.add(rect(f"{c}-bg", x, y, cw, ch, CARD, 18, LINE),
               ellipse(f"{c}-dot", x + 38, y + 44, 20, 20, GREY),
               text(f"{c}-name", name, x + 62, y + 26, 32, TEXT, HEAD),
               rich(f"{c}-detail", detail, x + 28, y + 82, 19, cw - 50, MUTED, MONO, line_height=1.2),
               spark(f"{c}-spark", x + 28, y + 136, cw - 56, 56, i * 1.7, status),
               group(c, [f"{c}-bg", f"{c}-dot", f"{c}-name", f"{c}-detail", f"{c}-spark"]))
        t = 500 + i * 140
        sc.pop(c, t, 450, 0.8)
        sc.keys(f"{c}-dot", "fill", [(0, GREY), (t + 500, GREY), (t + 700, status)], "linear")
        sc.keys(f"{c}-spark", "opacity", [(0, 0), (t + 500, 0), (t + 900, 1)])
    sc.add(rich("who", "Your router, NAS, Pi-hole, Home Assistant, websites, APIs and certificates.",
                120, 810, 36, 1680, MUTED, SEMI))
    sc.fade("who", 2600, dur=450, rise=14)
    sc.add(rich("every", [("Every reading gets its own ", None), ("status, chart and history.", TEXT)],
                120, 880, 36, 1680, MUTED, SEMI))
    sc.fade("every", 3100, dur=450, rise=14)
    return sc


# ----------------------------------------------------------------------------------------------- 4
def s04_dashboard():
    sc = Scene("04-dashboard", 5000)
    sc.add(backdrop(glow_at=(1300, 600)))
    kicker(sc, "02", "AT A GLANCE")
    headline(sc, [("One ", None), ("calm", TEAL), (" screen for all of it.", None)], t=100)
    sc.add(window("dash", "dashboard", 640, 270, 1180, 760, zoom=1.22, focus=(0.04, 0.12)))
    sc.pop("dash", 300, 600, 0.92)
    sc.keys("dash-img", "translate-x", [(0, 0), (900, 0), (5000, -220)], "linear")
    sc.keys("dash-img", "translate-y", [(0, 0), (900, 0), (5000, -90)], "linear")
    notes = [("Status first", "3 up · 3 degraded · 2 down, and what needs you", UP),
             ("Live charts", "latency and response time, from an hour to a year", TEAL),
             ("Your layout", "drag widgets, add tabs, pin the network map", GOLD)]
    for i, (title, body, colour) in enumerate(notes):
        y = 300 + i * 230
        n = f"note{i}"
        sc.add(rect(f"{n}-bar", 120, y, 6, 170, colour, 3),
               rich(f"{n}-title", title, 150, y, 42, 440, TEXT, HEAD),
               rich(f"{n}-body", body, 150, y + 58, 28, 440, MUTED, BODY, line_height=1.3),
               group(n, [f"{n}-bar", f"{n}-title", f"{n}-body"]))
        sc.fade(n, 900 + i * 700, dur=400)
        sc.slide(n, 900 + i * 700, dx=-50, dur=500)
    return sc


# ----------------------------------------------------------------------------------------------- 5
# The demo network from the interface's mock mode: each node sits under the node it depends on.
TREE = {"gw": ("Gateway", 790, 340, None),
        "sw": ("Office switch", 460, 500, "gw"), "ph": ("Pi-hole", 790, 500, "gw"), "px": ("Plex", 1120, 500, "gw"),
        "nas": ("NAS", 280, 680, "sw"), "ha": ("Home Assistant", 520, 680, "sw"),
        "cam": ("Garage camera", 760, 680, "sw"), "bk": ("Backup server", 1000, 680, "sw")}
NW, NH = 210, 62
T_DOWN, T_SPREAD, T_ALERT, T_UP = 1900, 2300, 2900, 5600


def node(sc, key, label, cx, cy):
    n = f"n-{key}"
    sc.add(rect(f"{n}-bg", cx - NW / 2, cy - NH / 2, NW, NH, ELEV, 10, LINE, 2),
           rect(f"{n}-bar", cx - NW / 2, cy - NH / 2, 6, NH, UP, 3),
           ellipse(f"{n}-dot", cx - NW / 2 + 28, cy, 14, 14, UP),
           text(f"{n}-name", label, cx - NW / 2 + 46, cy - 14, 24, TEXT, SEMI),
           group(n, [f"{n}-bg", f"{n}-bar", f"{n}-dot", f"{n}-name"]))
    return n


def s05_outage():
    sc = Scene("05-outage", 9000)
    sc.add(backdrop(glow_at=(700, 600), strength=0.4))
    kicker(sc, "03", "IN ACTION")
    headline(sc, [("One outage. ", None), ("One alert.", TEAL)], t=100)
    sc.add(rect("map-bg", 120, 260, 1140, 580, "#16181DCC", 22, LINE))
    sc.fade("map-bg", 200, dur=400)
    sc.add(text("map-title", "DEPENDENCIES", 150, 284, 20, MUTED, MONO))
    sc.fade("map-title", 200, dur=400)
    # Links first, so nodes sit on top of them.
    for key, (label, cx, cy, parent) in TREE.items():
        if not parent:
            continue
        _, px, py, _ = TREE[parent]
        mid = (py + NH / 2 + cy - NH / 2) / 2
        ln = f"link-{key}"
        sc.add(line(ln, [(px, py + NH / 2), (px, mid), (cx, mid), (cx, cy - NH / 2)], "#4A525C", 3))
        sc.fade(ln, 600, dur=400)
        depth = 1 if parent == "gw" else 2
        t_red = T_SPREAD + (depth - 1) * 350
        sc.keys(ln, "stroke_color", [(0, "#4A525C"), (t_red, "#4A525C"), (t_red + 250, DOWN),
                                     (T_UP, DOWN), (T_UP + 400, "#4A525C")], "linear")
    for i, (key, (label, cx, cy, parent)) in enumerate(TREE.items()):
        n = node(sc, key, label, cx, cy)
        sc.pop(n, 300 + i * 90, 420, 0.7)
        if key == "gw":
            for part, prop in ((f"{n}-bar", "fill"), (f"{n}-dot", "fill"), (f"{n}-bg", "stroke_color")):
                base = LINE if prop == "stroke_color" else UP
                sc.keys(part, prop, [(0, base), (T_DOWN, base), (T_DOWN + 150, DOWN), (T_UP, DOWN),
                                     (T_UP + 300, base)], "linear")
            sc.keys(f"{n}-bg", "fill", [(0, ELEV), (T_DOWN, ELEV), (T_DOWN + 150, "#3A1E22"), (T_UP, "#3A1E22"),
                                        (T_UP + 300, ELEV)], "linear")
            sc.keys(n, "translate-x", [(T_DOWN, 0), (T_DOWN + 60, -8), (T_DOWN + 120, 8), (T_DOWN + 180, -5),
                                      (T_DOWN + 240, 0)], "linear")
        else:
            depth = 1 if parent == "gw" else 2
            t_aff = T_SPREAD + (depth - 1) * 350 + 200
            for part in (f"{n}-bar", f"{n}-dot"):
                sc.keys(part, "fill", [(0, UP), (t_aff, UP), (t_aff + 200, GREY), (T_UP + 200, GREY),
                                       (T_UP + 500, UP)], "linear")
            sc.keys(f"{n}-name", "color", [(0, TEXT), (t_aff, TEXT), (t_aff + 200, MUTED), (T_UP + 200, MUTED),
                                           (T_UP + 500, TEXT)], "linear")
    sc.add(rect("ring", 790 - NW / 2 - 14, 340 - NH / 2 - 14, NW + 28, NH + 28, "#FF5C5C00", 16, DOWN, 3))
    sc.keys("ring", "opacity", [(0, 0), (T_DOWN, 0), (T_DOWN + 100, 1), (T_DOWN + 900, 0)], "ease-out-cubic")
    sc.keys("ring", "scale", [(T_DOWN, 1), (T_DOWN + 900, 1.35)], "ease-out-cubic")
    # Status line under the map.
    sc.add(text("status", "8 nodes  ·  all up", 150, 784, 24, UP, MONO))
    sc.fade("status", 900, dur=300)
    sc.key("status", "text", 0, "8 nodes  ·  all up")
    sc.key("status", "text", T_DOWN, "Gateway down  ·  7 nodes behind it: affected, not alerted")
    sc.key("status", "text", T_UP + 300, "Gateway recovered  ·  all up")
    sc.keys("status", "color", [(T_DOWN, UP), (T_DOWN + 100, DOWN), (T_UP + 300, DOWN), (T_UP + 400, UP)], "linear")

    # The notifications that actually arrive.
    nx, nw = 1310, 510

    def card(name, y, colour, title, body, meta):
        sc.add(rect(f"{name}-bg", nx, y, nw, 200, CARD, 20, LINE),
               rect(f"{name}-bar", nx, y + 24, 6, 152, colour, 3),
               text(f"{name}-meta", meta, nx + 36, y + 24, 20, MUTED, MONO),
               rich(f"{name}-title", title, nx + 36, y + 60, 38, nw - 70, TEXT, HEAD),
               rich(f"{name}-body", body, nx + 36, y + 112, 24, nw - 70, MUTED, BODY, line_height=1.3),
               shadow(f"{name}-bg", 40, 18, 0.5),
               group(name, [f"{name}-bg", f"{name}-bar", f"{name}-meta", f"{name}-title", f"{name}-body"]))

    sc.add(text("inbox", "YOUR INBOX", nx, 284, 20, MUTED, MONO))
    sc.fade("inbox", 200, dur=400)
    sc.add(rich("quiet", "Quiet. Nothing needs you.", nx, 340, 30, nw, MUTED, SEMI))
    sc.fade("quiet", 900, T_ALERT - 400, dur=300)
    card("mail1", 330, DOWN, "Gateway is down",
         "No reply from 192.168.1.1. Its 7 dependants are held back.", "GWATCH  ·  EMAIL  ·  NOW")
    sc.fade("mail1", T_ALERT, dur=300)
    sc.slide("mail1", T_ALERT, dx=120, dur=600)
    card("mail2", 560, UP, "Gateway recovered",
         "Down for 4 minutes. Everything behind it is back.", "GWATCH  ·  EMAIL  ·  4 MIN LATER")
    sc.fade("mail2", T_UP + 600, dur=300)
    sc.slide("mail2", T_UP + 600, dx=120, dur=600)
    sc.add(rich("count", [("11", DOWN), (" failing checks, ", None), ("1", TEXT), (" email.", None)],
                nx, 790, 30, nw, MUTED, SEMI))
    sc.fade("count", T_ALERT + 900, dur=400, rise=12)
    sc.add(rich("manners", "Dependency-aware, with cooldowns, silences and maintenance windows built in.",
                120, 880, 34, 1680, MUTED, SEMI))
    sc.fade("manners", 7000, dur=450, rise=14)
    return sc


# ----------------------------------------------------------------------------------------------- 6
def s06_map():
    sc = Scene("06-map", 4000)
    sc.add(backdrop(glow_at=(960, 700)))
    kicker(sc, "04", "NETWORK MAP")
    headline(sc, [("The same map, ", None), ("live in GWatch.", TEAL)], t=100)
    sc.add(window("map", "map", 120, 260, 1680, 760, zoom=1.12, focus=(0.035, 0.085)))
    sc.pop("map", 200, 600, 0.94)
    sc.keys("map-img", "translate-x", [(0, 0), (800, 0), (4000, -90)], "linear")
    sc.add(chip("hint", "Three layouts  ·  styled to taste  ·  pin it to a dashboard", 560, 940, 26, TEXT,
                "#0F1114E6", MONO, stroke=LINE, width=1000))
    sc.fade("hint", 1400, dur=400, rise=12)
    return sc


# ----------------------------------------------------------------------------------------------- 7
def s07_discover():
    sc = Scene("07-discover", 5000)
    sc.add(backdrop(glow_at=(1300, 500)))
    kicker(sc, "05", "SET UP IN MINUTES")
    headline(sc, [("Scan. Tick. ", None), ("Watched.", TEAL)], t=100)
    sc.add(window("disc", "discovery", 760, 270, 1060, 640, zoom=1.72, focus=(0.215, 0.195)))
    sc.pop("disc", 300, 600, 0.92)
    steps = [("01", "Type a range", "192.168.1.0/24"),
             ("02", "See what answers", "names, reply times, open ports"),
             ("03", "Tick and add", "a template suggested for each")]
    for i, (num, title, body) in enumerate(steps):
        y = 300 + i * 175
        s = f"step{i}"
        sc.add(text(f"{s}-num", num, 120, y + 6, 30, TEAL, MONO),
               rich(f"{s}-title", title, 200, y, 44, 520, TEXT, HEAD),
               rich(f"{s}-body", body, 200, y + 60, 28, 520, MUTED, BODY),
               group(s, [f"{s}-num", f"{s}-title", f"{s}-body"]))
        sc.fade(s, 800 + i * 650, dur=400)
        sc.slide(s, 800 + i * 650, dx=-50, dur=500)
    extras = ["No nmap, nothing to install", "SNMP: pick ports by name", "Pair a PC: hardware in a minute"]
    for i, label in enumerate(extras):
        c = f"x{i}"
        sc.add(chip(c, label, 120 + [0, 520, 1050][i], 960, 24, TEXT, ELEV, SEMI, stroke=LINE, dot=TEAL,
                    width=[440, 460, 500][i]))
        sc.pop(c, 3000 + i * 250, 400, 0.7)
    return sc


# ----------------------------------------------------------------------------------------------- 8
def s08_history():
    sc = Scene("08-history", 4000)
    sc.add(backdrop(glow_at=(500, 400)))
    kicker(sc, "06", "HISTORY")
    headline(sc, [("Remembers ", None), ("everything.", TEAL)], t=100)
    sc.add(window("node", "node", 820, 270, 1000, 740, zoom=1.45, focus=(0.02, 0.06)))
    sc.pop("node", 200, 600, 0.92)
    sc.keys("node-img", "translate-y", [(0, 0), (800, 0), (4000, -110)], "linear")
    items = [("Timestacked charts", "today laid over yesterday, last week, last month"),
             ("Incidents", "grouped, acknowledged, with notes and timing"),
             ("Availability reports", "weekly or monthly, by email"),
             ("Years of history", "rolled up, so the database never balloons")]
    for i, (title, body) in enumerate(items):
        y = 290 + i * 180
        it = f"item{i}"
        sc.add(ellipse(f"{it}-dot", 132, y + 24, 16, 16, TEAL),
               rich(f"{it}-title", title, 160, y, 40, 620, TEXT, HEAD),
               rich(f"{it}-body", body, 160, y + 54, 27, 620, MUTED, BODY),
               group(it, [f"{it}-dot", f"{it}-title", f"{it}-body"]))
        sc.fade(it, 500 + i * 500, dur=350)
        sc.slide(it, 500 + i * 500, dx=-40, dur=450)
    return sc


# ----------------------------------------------------------------------------------------------- 9
RECIPES = [("When Plex goes down", "run  docker restart plex", "ran 2 h ago  ·  Plex was back in 40 s", UP),
           ("When 2 of 3 DNS servers fail", "notify  ntfy  +  Pushover", "a rule across nodes, any or at-least-N", WARN),
           ("When the router reboots", "POST /hook/router-rebooted", "re-runs the Gateway's checks at once", TEAL)]
NOTIFY = ["Email", "Webhooks", "Slack", "Teams", "ntfy", "Pushover", "git", "Scripts"]


def s09_automate():
    sc = Scene("09-automate", 6000)
    sc.add(backdrop(glow_at=(1500, 500)))
    kicker(sc, "07", "AUTOMATION")
    headline(sc, [("It doesn't just watch. ", None), ("It acts.", TEAL)], t=100)
    for i, (when, then, note, colour) in enumerate(RECIPES):
        y = 280 + i * 190
        r = f"rec{i}"
        sc.add(rect(f"{r}-bg", 120, y, 1000, 166, CARD, 20, LINE),
               rect(f"{r}-bar", 120, y + 22, 6, 122, colour, 3),
               text(f"{r}-when", when, 156, y + 22, 38, TEXT, HEAD),
               chip(f"{r}-then", then, 156, y + 78, 24, BG, TEAL, MONO, width=round(len(then) * 15.5 + 60)),
               text(f"{r}-note", note, 156 + round(len(then) * 15.5 + 60) + 24, y + 90, 22, MUTED, BODY),
               group(r, [f"{r}-bg", f"{r}-bar", f"{r}-when", f"{r}-then", f"{r}-note"]))
        t = 600 + i * 900
        sc.fade(r, t, dur=350)
        sc.slide(r, t, dx=-60, dur=550)
        sc.pop(f"{r}-then", t + 350, 400, 0.6)
    sc.add(window("auto", "automation", 1180, 280, 640, 420, zoom=2.0, focus=(0.22, 0.335)))
    sc.pop("auto", 400, 600, 0.9)
    sc.keys("auto-img", "translate-x", [(0, 0), (1000, 0), (6000, -120)], "linear")
    x = 120
    for i, label in enumerate(NOTIFY):
        w = round(len(label) * 15 + 64)
        sc.add(chip(f"nt{i}", label, x, 900, 26, TEXT, ELEV, SEMI, stroke=LINE, width=w))
        sc.pop(f"nt{i}", 3400 + i * 150, 380, 0.5)
        x += w + 16
    return sc


# ----------------------------------------------------------------------------------------------- 10
USES = [("Homelab", "Servers, NAS, Plex and Pi-hole, watched from one place.", TEAL),
        ("Smart home", "Know Home Assistant is up, and tell it when something isn't.", GOLD),
        ("Websites & APIs", "Response times, keywords, JSON values and certificate expiry.", UP),
        ("Every PC", "A tiny agent reports CPU, memory and disks from any machine.", "#7AA7FF"),
        ("Small office", "Switches and access points over SNMP, plus availability reports.", WARN),
        ("A spare screen", "A wallboard for the monitor or tablet on the wall.", "#FF8FB1")]


def s10_uses():
    sc = Scene("10-uses", 5000)
    sc.add(backdrop(glow_at=(960, 650)))
    kicker(sc, "08", "MADE FOR")
    headline(sc, [("Homelabs, families ", None), ("and small offices.", TEAL)], t=100)
    tw, th, gap = 540, 300, 30
    for i, (title, body, colour) in enumerate(USES):
        x = 120 + (i % 3) * (tw + gap)
        y = 290 + (i // 3) * (th + gap)
        u = f"use{i}"
        sc.add(rect(f"{u}-bg", x, y, tw, th, CARD, 22, LINE),
               rect(f"{u}-top", x + 34, y + 34, 54, 8, colour, 4),
               rich(f"{u}-title", title, x + 34, y + 68, 46, tw - 68, TEXT, HEAD),
               rich(f"{u}-body", body, x + 34, y + 140, 28, tw - 68, MUTED, BODY, line_height=1.32),
               group(u, [f"{u}-bg", f"{u}-top", f"{u}-title", f"{u}-body"]))
        sc.pop(u, 400 + i * 220, 480, 0.85)
    # The last tile's description gives way to the real wallboard.
    sc.keys("use5-body", "opacity", [(0, 1), (2800, 1), (3100, 0)])
    sc.add(window("wall", "wall", 120 + 2 * (tw + gap) + 34, 290 + th + gap + 138, tw - 68, 136, bar=22))
    sc.keys("wall", "opacity", [(0, 0), (2900, 0), (3300, 1)])
    sc.slide("wall", 2900, dy=30, dur=500)
    return sc


# ----------------------------------------------------------------------------------------------- 11
TRUST = [("No cloud account", "Nothing is phoned home. It never exposes itself to the internet."),
         ("Signed updates", "Every release is signed and verified before it installs."),
         ("Encrypted backups", "One click, password-protected, restorable on a new machine."),
         ("For everyone", "Keyboard- and screen-reader-accessible, with tables for every chart.")]
PLATFORMS = ["Windows", "Linux", "macOS", "Docker", "Raspberry Pi"]


def s11_trust():
    sc = Scene("11-trust", 4000)
    sc.add(backdrop(glow_at=(400, 300)))
    kicker(sc, "09", "STAYS HOME")
    headline(sc, [("Your network. ", None), ("Your data.", TEAL)], t=100)
    cw = 405
    for i, (title, body) in enumerate(TRUST):
        x = 120 + i * (cw + 20)
        t = f"tr{i}"
        sc.add(rect(f"{t}-bg", x, 290, cw, 380, CARD, 22, LINE),
               text(f"{t}-num", f"0{i + 1}", x + 34, 324, 26, TEAL, MONO),
               rich(f"{t}-title", title, x + 34, 380, 44, cw - 60, TEXT, HEAD, line_height=1.05),
               rich(f"{t}-body", body, x + 34, 490, 27, cw - 60, MUTED, BODY, line_height=1.32),
               group(t, [f"{t}-bg", f"{t}-num", f"{t}-title", f"{t}-body"]))
        sc.pop(t, 300 + i * 300, 480, 0.85)
        sc.slide(t, 300 + i * 300, dy=40, dur=550)
    sc.add(text("runs", "RUNS ON", 120, 760, 24, MUTED, MONO))
    sc.fade("runs", 1700, dur=300)
    x = 280
    for i, label in enumerate(PLATFORMS):
        w = round(len(label) * 15.5 + 64)
        sc.add(chip(f"pf{i}", label, x, 742, 28, TEXT, ELEV, SEMI, stroke=LINE, width=w))
        sc.pop(f"pf{i}", 1800 + i * 130, 380, 0.6)
        x += w + 16
    sc.add(rich("db", "SQLite built in, or your own PostgreSQL or MySQL server.", 120, 860, 34, 1680, MUTED, SEMI))
    sc.fade("db", 2600, dur=400, rise=12)
    return sc


# ----------------------------------------------------------------------------------------------- 12
def s12_end():
    sc = Scene("12-end", 4000)
    sc.add(backdrop(glow_at=(960, 420), strength=0.5))
    sc.add(glow("pulse", 960, 330, 900, TEAL, "45"))
    sc.add(mark("m", 590, 220, 200))
    sc.add(text("word", "GWatch", 820, 236, 150, TEXT, HEAD))
    sc.add(group("logo", ["m", "word"]))
    sc.pop("logo", 0, 600, 0.6)
    sc.add(centered("line", [("Your home network, ", None), ("watched calmly.", TEAL)], 500, 84))
    sc.words("line", 400, "fade-up", "word", 480, 90)
    sc.add(chip("badge", f"FREE TO USE  ·  BETA {VERSION}", 960 - 230, 650, 26, NAVY, GOLD, MONO, width=460))
    sc.pop("badge", 1100, 450, 0.6)
    sc.add(centered("url", REPO, 760, 46, TEAL, MONO))
    sc.fade("url", 1400, dur=400, rise=16)
    sc.add(centered("made", "Every frame of this video is an editable Vixl document.", 975, 24, MUTED, BODY))
    sc.fade("made", 2000, dur=500)
    return sc


SCENES = [s01_hook, s02_logo, s03_checks, s04_dashboard, s05_outage, s06_map, s07_discover, s08_history,
          s09_automate, s10_uses, s11_trust, s12_end]
