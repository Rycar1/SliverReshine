#!/usr/bin/env python3
"""Report i18n keys that a component declares in the locale file but never uses,
and keys it uses but which are missing from the locale. Run from frontend/."""
import re
import sys
from pathlib import Path

SRC = Path("src")
EN = SRC / "i18n" / "locales" / "en.ts"


def declared(section: str) -> set[str]:
    """Top-level keys of a `section: { ... }` block, one level deep."""
    text = EN.read_text(encoding="utf-8")
    m = re.search(rf"^  {re.escape(section)}: \{{$", text, re.M)
    if not m:
        return set()
    out: set[str] = set()
    depth = 1
    for line in text[m.end():].splitlines():
        if re.match(r"^  \},?$", line):
            break
        # `key: 'value'` on one line, or `key:` with the value on the next.
        if re.match(r"^\s{4}[a-zA-Z0-9_]+:(\s|$)", line):
            out.add(re.match(r"^\s{4}([a-zA-Z0-9_]+):", line).group(1))
        depth += line.count("{") - line.count("}")
    return out


def used(section: str) -> set[str]:
    keys: set[str] = set()
    for path in SRC.rglob("*"):
        if path.suffix not in (".ts", ".tsx") or "locales" in path.parts:
            continue
        for m in re.finditer(rf"['\"`]({re.escape(section)}\.([a-zA-Z0-9_.]+))['\"`]", path.read_text(encoding="utf-8")):
            keys.add(m.group(2).split(".")[0])
        # Dynamic prefixes like postex.pane.${p} make every pane key reachable.
        if re.search(rf"{re.escape(section)}\.[a-zA-Z.]*\$\{{", path.read_text(encoding="utf-8")):
            keys.add("*dynamic*")
    return keys


bad = 0
for section in sys.argv[1:] or ["postex", "files", "portfwd"]:
    dec, use = declared(section), used(section)
    dynamic = "*dynamic*" in use
    use.discard("*dynamic*")
    unused = sorted(dec - use - ({"pane"} if dynamic else set()))
    missing = sorted(use - dec)
    print(f"{section}: declared={len(dec)} used={len(use)}")
    for k in unused:
        print(f"  UNUSED  {section}.{k}")
        bad += 1
    for k in missing:
        print(f"  MISSING {section}.{k}")
        bad += 1

print("clean" if not bad else f"{bad} problem(s)")
sys.exit(1 if bad else 0)
