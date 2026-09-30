#!/usr/bin/env python3
"""Confirm every translation key the UI asks for exists in the rebuilt locales.

The locale files were regenerated from a bundle built before the refactors, so
the thing that could have gone wrong is a component using a key the rebuild did
not carry over. i18next does not throw for a missing key -- it renders the key
itself -- so nothing else in the build would catch it.

Collects t('...') / i18nKey="..." style usages across src/ and resolves each
against the flattened locale object. Run from frontend/.
"""
import json
import re
import sys
from pathlib import Path

SRC = Path("src")
LOCALES = SRC / "i18n" / "locales"


def parse_locale(path: Path) -> dict:
    """Read a locale .ts back into a nested dict.

    The files are generated in a fixed style, so a small line parser is enough
    and avoids pulling a TS toolchain in.
    """
    root: dict = {}
    stack: list[tuple[int, dict]] = [(-1, root)]
    for raw in path.read_text(encoding="utf-8").splitlines():
        line = raw.rstrip("\r")
        stripped = line.strip()
        if not stripped or stripped.startswith("export default"):
            continue
        indent = (len(line) - len(line.lstrip(" "))) // 2
        while len(stack) > 1 and stack[-1][0] >= indent:
            stack.pop()
        if stripped in ("},", "}"):
            continue
        m = re.match(r"^([a-zA-Z0-9_]+):\s*\{(.*)$", stripped)
        if m:
            node: dict = {}
            stack[-1][1][m.group(1)] = node
            stack.append((indent, node))
            continue
        m = re.match(r"^([a-zA-Z0-9_]+):\s*'(.*)'\s*,?$", stripped)
        if m:
            val = m.group(2).replace("\\'", "'").replace("\\\\", "\\")
            stack[-1][1][m.group(1)] = val
            continue
    return root


def leaves(obj, prefix=""):
    out = {}
    for k, v in obj.items():
        if isinstance(v, dict):
            out.update(leaves(v, f"{prefix}.{k}" if prefix else k))
        else:
            out[prefix + "." + k if prefix else k] = v
    return out


en = leaves(parse_locale(LOCALES / "en.ts"))
zh = leaves(parse_locale(LOCALES / "zh.ts"))
print(f"en: {len(en)} keys, zh: {len(zh)} keys")
if set(en) != set(zh):
    print("PARITY FAILURE")
    print("  en only:", sorted(set(en) - set(zh))[:20])
    print("  zh only:", sorted(set(zh) - set(en))[:20])
    sys.exit(1)

# Every t('literal') call, and any backtick-free template we can resolve.
used: set[str] = set()
dynamic_prefixes: set[str] = set()
for path in list(SRC.rglob("*.ts")) + list(SRC.rglob("*.tsx")):
    if "locales" in path.parts:
        continue
    text = path.read_text(encoding="utf-8")
    # A real t() call, not the tail of format(/split(/test(.
    for m in re.finditer(r"""(?<![A-Za-z0-9_$.])t\(\s*['\"]([a-zA-Z0-9_.]+)['\"]""", text):
        used.add(m.group(1))
    for m in re.finditer(r"""i18nKey=\{?\s*['"]([a-zA-Z0-9_.]+)['"]""", text):
        used.add(m.group(1))
    # t(`section.pane.${x}`) -- record the prefix, it is legitimately dynamic.
    for m in re.finditer(r"""t\(\s*`([a-zA-Z0-9_.]*)\$\{""", text):
        dynamic_prefixes.add(m.group(1))

missing = sorted(k for k in used if k not in en)
print(f"literal keys referenced in src: {len(used)}")
print(f"dynamic prefixes: {sorted(dynamic_prefixes) or 'none'}")
if missing:
    print(f"\nMISSING FROM LOCALES ({len(missing)}):")
    for k in missing:
        print(f"  {k}")
    sys.exit(1)
print("\nall referenced keys present in both locales")
