#!/usr/bin/env python3
"""Checks the relative links and #anchors in the repository's Markdown files.

    python3 scripts/ci/check-md-links.py [root]

Every [text](target) and <a href="target"> whose target is not a URL must name
a file or directory that exists, and a #fragment must match a heading in the
target Markdown file, slugged the way GitHub does it (or an explicit
<a id="..."> / <a name="..."> anchor). Links inside code spans and fenced code
blocks are ignored, and so is .release/notes (see SKIP_PATHS). Exits 1 and
lists every broken link if there are any.
"""
import os
import re
import sys
import urllib.parse

SKIP_DIRS = {".git", "node_modules", ".claude", "dist", ".toolkit"}
# Release notes become GitHub release bodies, where a relative link does not
# resolve against the repository anyway; the published ones are left as they are.
SKIP_PATHS = {os.path.join(".release", "notes")}
LINK_RE = re.compile(r"!?\[(?:[^\[\]]|\[[^\]]*\])*\]\(\s*<?([^)\s>]+)>?(?:\s+\"[^\"]*\")?\s*\)")
HREF_RE = re.compile(r"""<a\s[^>]*href=["']([^"']+)["']""", re.I)
ANCHOR_RE = re.compile(r"""<a\s[^>]*(?:id|name)=["']([^"']+)["']""", re.I)
HEADING_RE = re.compile(r"^(#{1,6})\s+(.*?)\s*#*\s*$")
FENCE_RE = re.compile(r"^\s*(```|~~~)")


def strip_code(lines, spans=True):
    """Yields (lineno, line) with fenced blocks blanked and (if spans) code spans removed."""
    fence = None
    for i, line in enumerate(lines, 1):
        m = FENCE_RE.match(line)
        if m:
            if fence is None:
                fence = m.group(1)
            elif m.group(1) == fence:
                fence = None
            yield i, ""
            continue
        if fence is not None:
            yield i, ""
            continue
        yield i, re.sub(r"`+[^`]*`+", "", line) if spans else line


def slug(text):
    text = re.sub(r"<[^>]+>", "", text)
    text = re.sub(r"!?\[([^\]]*)\]\([^)]*\)", r"\1", text)
    text = text.replace("`", "").strip().lower()
    text = re.sub(r"[^\w\- ]", "", text)
    return text.replace(" ", "-")


_anchors = {}


def anchors(path):
    if path not in _anchors:
        found, seen = set(), {}
        with open(path, encoding="utf-8") as f:
            lines = f.read().splitlines()
        for _, line in strip_code(lines, spans=False):
            for a in ANCHOR_RE.findall(re.sub(r"`+[^`]*`+", "", line)):
                found.add(a)
            m = HEADING_RE.match(line)
            if m:
                s = slug(m.group(2))
                n = seen.get(s, 0)
                seen[s] = n + 1
                found.add(s if n == 0 else f"{s}-{n}")
        _anchors[path] = found
    return _anchors[path]


def main(root):
    root = os.path.abspath(root)
    broken = []
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [
            d for d in dirnames
            if d not in SKIP_DIRS and os.path.relpath(os.path.join(dirpath, d), root) not in SKIP_PATHS
        ]
        for name in filenames:
            if not name.lower().endswith(".md"):
                continue
            src = os.path.join(dirpath, name)
            with open(src, encoding="utf-8") as f:
                lines = f.read().splitlines()
            for lineno, line in strip_code(lines):
                for target in LINK_RE.findall(line) + HREF_RE.findall(line):
                    if re.match(r"^[a-z][a-z0-9+.-]*:", target, re.I) or target.startswith("//"):
                        continue
                    path, _, frag = target.partition("#")
                    path = urllib.parse.unquote(path)
                    dest = src if not path else os.path.normpath(os.path.join(dirpath, path))
                    rel = os.path.relpath(src, root)
                    if not os.path.exists(dest):
                        broken.append(f"{rel}:{lineno}: {target}: no such file")
                        continue
                    if frag and dest.lower().endswith(".md") and os.path.isfile(dest):
                        if urllib.parse.unquote(frag) not in anchors(dest):
                            broken.append(f"{rel}:{lineno}: {target}: no such heading")
    for b in broken:
        print(b)
    if broken:
        print(f"{len(broken)} broken Markdown link(s)", file=sys.stderr)
        return 1
    print("Markdown links: all relative links and anchors resolve")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1] if len(sys.argv) > 1 else "."))
