#!/usr/bin/env python3
"""Render ANSI-colored terminal output as an SVG "screenshot" for the README.

    CLICOLOR_FORCE=1 kubectl why deploy/api | python3 hack/ansi2svg.py "kubectl why deploy/api -n shop" > docs/demo.svg
"""
import html, re, sys

COLS, CW, LH, PAD, TOP = 112, 8.4, 19, 18, 44
PALETTE = {"31": "#ff6b6b", "32": "#8ef0c6", "33": "#ffcf70", "34": "#7aa2ff", "36": "#6fd3ff"}
FG, DIM, BG = "#e6e8ec", "#7d838e", "#0d1117"

def spans(line):
    """Split one line into (text, style) runs."""
    out, style = [], set()
    for part in re.split(r"(\x1b\[[0-9;]*m)", line):
        m = re.fullmatch(r"\x1b\[([0-9;]*)m", part)
        if m:
            codes = m.group(1).split(";") if m.group(1) else ["0"]
            style = set() if "0" in codes else style | set(codes)
        elif part:
            out.append((part, frozenset(style)))
    return out

def wrap(runs):
    """Soft-wrap runs at COLS, continuing with the line's indent + 2."""
    text = "".join(t for t, _ in runs)
    indent = len(text) - len(text.lstrip(" ")) + 2
    lines, cur, n = [], [], 0
    for t, s in runs:
        for word in re.split(r"(\s+)", t):
            if not word:
                continue
            if n + len(word) > COLS and n > indent and not word.isspace():
                lines.append(cur); cur = [(" " * indent, frozenset())]; n = indent
            if word.isspace() and n == indent and lines:
                continue
            cur.append((word, s)); n += len(word)
    lines.append(cur)
    return lines

def main():
    cmd = sys.argv[1] if len(sys.argv) > 1 else ""
    raw = sys.stdin.read().rstrip("\n").split("\n")
    rows = [[("$ ", frozenset({"32"})), (cmd, frozenset({"1"}))], []]
    for l in raw:
        rows.extend(wrap(spans(l)))
    w = int(COLS * CW + PAD * 2)
    h = int(TOP + len(rows) * LH + PAD)
    o = [f'<svg xmlns="http://www.w3.org/2000/svg" width="{w}" height="{h}" viewBox="0 0 {w} {h}" font-family="ui-monospace,SFMono-Regular,Menlo,Consolas,monospace" font-size="13.5">',
         f'<rect width="{w}" height="{h}" rx="10" fill="{BG}"/>',
         f'<rect x=".5" y=".5" width="{w-1}" height="{h-1}" rx="10" fill="none" stroke="#2a2e36"/>']
    for i, c in enumerate(("#ff5f57", "#febc2e", "#28c840")):
        o.append(f'<circle cx="{20 + i * 20}" cy="18" r="6" fill="{c}"/>')
    o.append(f'<text x="{w/2}" y="22" fill="{DIM}" text-anchor="middle" font-size="12">kubectl why</text>')
    for r, runs in enumerate(rows):
        y = TOP + r * LH + 12
        tspans = []
        for t, s in runs:
            fill = next((PALETTE[c] for c in s if c in PALETTE), DIM if "2" in s else FG)
            weight = ' font-weight="700"' if "1" in s else ""
            tspans.append(f'<tspan fill="{fill}"{weight}>{html.escape(t)}</tspan>')
        if tspans:
            o.append(f'<text x="{PAD}" y="{y}" xml:space="preserve">{"".join(tspans)}</text>')
    o.append("</svg>")
    print("\n".join(o))

if __name__ == "__main__":
    main()
