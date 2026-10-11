# GWatch in 60 seconds

**Your home network, watched calmly.** A one-minute marketing video for GWatch, told as one night on a
home network and drawn in the interface's own visual language. It is made with
[Vixl](https://github.com/jxburros/Vixl): every shot is an editable `.vixl` master with a keyframe
timeline, the interface shots are the real GWatch web interface, the logo is the kit in
[`brand/`](../../brand/README.md), and the soundtrack is synthesised in code.

**[`gwatch-in-60-seconds.mp4`](gwatch-in-60-seconds.mp4)** · 60.0 s · 1920×1080 · 30 fps · AAC stereo 48 kHz · 6.1 MB

![Contact sheet, one frame every two seconds](contact-sheet.jpg)

## The look

The video borrows from `web/app.css` ("Signal") rather than from a slide template:

- **Warm graphite and square corners.** The surface, card and line colours are the interface's dark
  theme tokens. Nothing is rounded, as in the product.
- **Panels like dashboard widgets.** A card surface, a hairline border, a header band with a tracked
  Kode Mono label, and the short teal tick each widget has at its top-left corner.
- **Barlow for words, Kode Mono for every number and label.** The status numerals are large and
  tabular, as on the Overall health widget.
- **Glow means status, and only status.** Status dots, the down gateway and lit numerals glow. Nothing
  decorative does.
- **The interface's own furniture.** The left rail is cropped from a real screenshot, the backdrop is
  the faint grid with a teal wash, and the reticle that picks out parts of the interface comes from
  `web/signal-reticle.svg`.

## The cut

The story runs on a clock: the gateway drops at 23:42, and at 07:00 everything is quietly up again.

| Time | Shot | What happens |
| --- | --- | --- |
| 0:00 | `01-night` | A single green status dot. *23:41:58 · All 8 nodes up.* It turns red: *23:42:03 · Gateway › Ping · Down.* "The Wi-Fi just dropped." The dot then travels to where the gateway sits in the next shot |
| 0:04 | `02-outage` | The console assembles around it, with the real rail, a top bar and a running clock. The red spreads through the dependency tree and the seven nodes behind the gateway are marked *affected*. Overall health reads 0 · 1 · 7. On the *Alerts sent* timeline, 11 failing checks sit below the line and one email above it. At 23:46 the gateway recovers: the numbers go back to 8 · 0 · 0, the all-clear email lands and *Needs attention* says "Nothing needs you." Then: *One outage. One alert.* |
| 0:17 | `03-title` | The reticle closes on the mark, the eye looks around, and the title reads *GWatch. Your home network, watched calmly.* |
| 0:21 | `04-checks` | *Ten kinds of check. One quiet list.* One live row per check type, with values ticking and sparklines |
| 0:27 | `05-tour` | The real interface at full frame, no window chrome. The reticle travels over the dashboard (status, charts), the network map (tree, customise) and discovery (scan, tick and add), with a caption card |
| 0:39 | `06-automate` | *It doesn't just watch. It acts.* An event log: a trigger runs `docker restart plex`, a rule notifies ntfy and Pushover, and an inbound endpoint re-runs the gateway's checks. Below it, the eight things it can do |
| 0:45 | `07-for` | Over the real wallboard: *For the homelab. For the smart home. For the family website. For the small office. For the screen on the wall.* Then the wallboard itself |
| 0:51 | `08-home` | *Stays home.* An *About* panel: no cloud account, never phones home, signed updates, encrypted backups, accessible, runs on five platforms, three databases, free to use |
| 0:56 | `09-end` | Lockup, tagline, *Free to use · Beta 0.6.0*, the repository. In the corner: *07:00:00 · All 8 nodes up* |

The outage in shot 2 is drawn as Vixl vectors from the interface's demo network (the gateway and the
nodes that depend on it), so every status change can animate. The music follows the same story: a
clock ticks through the cold open, a heartbeat holds through the outage, the first major chord lands
with the recovery, and the groove starts at the title card.

## Facts on screen

Every claim comes from the 0.6.0 README, CHANGELOG and `docs/`, the same list the beta kit uses
([`../beta/README.md`](../beta/README.md#how-its-built)). If one changes, edit `build/scenes.py`
(`CHECKS`, `EVENTS`, `ACTIONS`, `FOR`, `ABOUT`) and rebuild. The version on the end card comes from
`VERSION` in `../beta/build.py`. As in the kit, the copy says "free to use", not "open source": the
[GWatch Community License](../../LICENSE) is source-available.

## What is in this folder

| Path | What it is |
| --- | --- |
| `gwatch-in-60-seconds.mp4` | The finished video |
| `contact-sheet.jpg` | One frame every two seconds |
| `scenes/*.vixl` | Nine editable 1920×1080 masters with keyframe timelines, fonts and screenshots embedded |
| `film-spec.json` | The `film-export` request: shot order, durations and the two-track audio mix |
| `build/kit.py` | The interface's tokens and components as Vixl operations: panels, tags, status dots and glows, the grid backdrop, the reticle, the rail |
| `build/scenes.py` | The nine shots, and the clock that drives shot 2 |
| `build/score.py` | A numpy synthesiser: the score and the effects, cued to the same story times |
| `build/make_video.py` | Builds the scenes, writes `audio/*.wav` (not committed) and runs `film-export` |
| `build/preview.py` | Writes a contact sheet per shot for quick review |
| `screenshots.mjs`, `screens/` | The discovery screenshot from the interface's demo mode (`?mock=1`). The dashboard, map and wallboard shots come from `../beta/screens/` |

## Rebuild

You need Python 3.11+, `ffmpeg` on `PATH`, Vixl 0.25+, and network access the first time, when
Vixl downloads Barlow and Kode Mono.

```bash
npm ci                                            # once: Playwright, for the screenshots
node marketing/video/screenshots.mjs              # optional: refresh screens/*.png
python -m pip install 'vixl[pdf] @ https://github.com/jxburros/Vixl/archive/refs/tags/v0.25.0.tar.gz'
python marketing/video/build/make_video.py        # scenes, audio and the MP4 (about 7 minutes on 4 cores)
python marketing/video/build/make_video.py --scenes
cd marketing/video/build && python preview.py /tmp 02 05   # contact sheets for selected shots
```

To change one shot without rebuilding everything, edit its master, for example
`vixl -p marketing/video/scenes/02-outage.vixl render --time 3s --out frame.png`, then run
`vixl workflow film-export --request marketing/video/film-spec.json --workspace marketing/video`.
Re-running `make_video.py` overwrites the masters.
