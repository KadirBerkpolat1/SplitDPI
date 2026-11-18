#!/usr/bin/env bash
# Renders every TUI screen to PNG without root or a running service.
# TestDumpScreens (internal/tui/screens_test.go) writes each screen as ANSI on
# the fake backend; each one is laid out on a grid of terminal cells, shot
# with headless Chromium, and put on one contact sheet per language and size.
#
# usage: scripts/tui-screens.sh OUTDIR
#   OUTDIR/<lang>-<W>x<H>-<screen>.png  one screen
#   OUTDIR/sheet-<lang>-<W>x<H>.png     every screen of that language and size
# needs: go, python3, chromium (or google-chrome-stable), magick, fc-match
set -euo pipefail

if [ $# -ne 1 ]; then
	echo "usage: $0 OUTDIR" >&2
	exit 2
fi

chrome=$(command -v chromium || command -v google-chrome-stable || true)
if [ -z "$chrome" ]; then
	echo "$0: chromium or google-chrome-stable is needed" >&2
	exit 1
fi
for c in go python3 magick fc-match; do
	if ! command -v "$c" >/dev/null; then
		echo "$0: $c is needed" >&2
		exit 1
	fi
done

# One terminal cell in pixels, and the font size that fills it.
cell_w=9 cell_h=18 font_px=15

root=$(cd "$(dirname "$0")/.." && pwd)
mkdir -p "$1"
out=$(cd "$1" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

(cd "$root" && SPLITDPI_SCREENS_DIR="$work/ans" go test ./internal/tui/ -run '^TestDumpScreens$' -count=1)

# ANSI (SGR only) to HTML, one absolutely sized cell per character. Prints
# "<html> <png> <width px> <height px>" per screen, sorted by name.
python3 - "$work/ans" "$work/html" "$cell_w" "$cell_h" "$font_px" >"$work/list" <<'PY'
import html, os, re, sys, unicodedata

src, dst, cw, ch, fpx = sys.argv[1], sys.argv[2], *map(int, sys.argv[3:6])
os.makedirs(dst)
FG, BG = (208, 208, 208), (18, 18, 20)
BASE = [(0, 0, 0), (205, 0, 0), (0, 205, 0), (205, 205, 0), (0, 0, 238), (205, 0, 205), (0, 205, 205), (229, 229, 229),
        (127, 127, 127), (255, 0, 0), (0, 255, 0), (255, 255, 0), (92, 92, 255), (255, 0, 255), (0, 255, 255), (255, 255, 255)]

def c256(n):
    if n < 16:
        return BASE[n]
    if n < 232:
        n -= 16
        return tuple(0 if v == 0 else 55 + 40 * v for v in (n // 36, n // 6 % 6, n % 6))
    return (8 + 10 * (n - 232),) * 3

def color(p, i):
    if p[i] == 5 and i + 1 < len(p):
        return c256(p[i + 1]), i + 2
    if p[i] == 2 and i + 3 < len(p):
        return tuple(p[i + 1:i + 4]), i + 4
    return None, len(p)

def sgr(st, params):
    p = [int(x) if x else 0 for x in params.replace(":", ";").split(";")]
    i = 0
    while i < len(p):
        c, i = p[i], i + 1
        if c == 0:
            st.clear()
        elif c in (1, 2, 3, 4, 7, 9):
            st[c] = True
        elif c == 22:
            st.pop(1, None), st.pop(2, None)
        elif c in (23, 24, 27, 29):
            st.pop(c - 20, None)
        elif 30 <= c <= 37 or 90 <= c <= 97:
            st["fg"] = BASE[c - 30 if c < 90 else c - 82]
        elif 40 <= c <= 47 or 100 <= c <= 107:
            st["bg"] = BASE[c - 40 if c < 100 else c - 92]
        elif c in (38, 48) and i < len(p):
            rgb, i = color(p, i)
            if rgb:
                st["fg" if c == 38 else "bg"] = rgb
        elif c == 39:
            st.pop("fg", None)
        elif c == 49:
            st.pop("bg", None)

def css(st):
    fg, bg = st.get("fg", FG), st.get("bg")
    if st.get(7):
        fg, bg = bg or BG, fg
    if st.get(2):
        base = bg or BG
        fg = tuple((a + b) // 2 for a, b in zip(fg, base))
    out = ["color:#%02x%02x%02x" % fg]
    if bg:
        out.append("background:#%02x%02x%02x" % bg)
    if st.get(1):
        out.append("font-weight:bold")
    if st.get(3):
        out.append("font-style:italic")
    deco = [d for k, d in ((4, "underline"), (9, "line-through")) if st.get(k)]
    if deco:
        out.append("text-decoration:" + " ".join(deco))
    return ";".join(out)

esc = re.compile(r"\x1b(?:\[([0-9;:?]*)([A-Za-z])|\][^\x07\x1b]*(?:\x07|\x1b\\)|.)")
name_re = re.compile(r"^[a-z]+-(\d+)x(\d+)-.+\.ans$")

for f in sorted(os.listdir(src)):
    m = name_re.match(f)
    if not m:
        continue
    w, h = int(m[1]), int(m[2])
    text = open(os.path.join(src, f), encoding="utf-8").read().removesuffix("\n")
    rows, st = [], {}
    for line in text.split("\n"):
        cells, pos = [], 0
        for e in esc.finditer(line + "\x1b["):
            for chr_ in line[pos:e.start()]:
                width = 2 if unicodedata.east_asian_width(chr_) in "WF" else 0 if unicodedata.combining(chr_) else 1
                if width == 0 and cells:
                    cells[-1] = (cells[-1][0] + chr_, cells[-1][1], cells[-1][2])
                elif width:
                    cells.append((chr_, width, css(st)))
            if e.group(2) == "m":
                sgr(st, e.group(1))
            pos = e.end()
        rows.append(cells)
    cols = max(sum(c[1] for c in r) for r in rows)
    if cols > w or len(rows) != h:
        print(f"{f}: {cols} columns x {len(rows)} lines, terminal is {w}x{h}", file=sys.stderr)
    body = []
    for r in rows:
        spans, run, style = [], [], None
        for chr_, width, s in r + [("", 0, None)]:
            if s != style and run:
                spans.append(f'<span style="{style}">{"".join(run)}</span>')
                run = []
            style = s
            if width:
                run.append(f'<i{" class=w" if width == 2 else ""}>{html.escape(chr_)}</i>')
        body.append("<div>" + "".join(spans) + "</div>")
    page = f"""<!doctype html><meta charset=utf-8><style>
html,body{{margin:0;background:#{"%02x%02x%02x" % BG};overflow:hidden}}
body{{width:{w * cw}px;height:{h * ch}px;font:{fpx}px/{ch}px "JetBrainsMono Nerd Font Mono","DejaVu Sans Mono","Noto Sans Mono",monospace;color:#{"%02x%02x%02x" % FG}}}
div{{height:{ch}px;white-space:pre}}
span{{display:inline-block;height:{ch}px;vertical-align:top}}
i{{display:inline-block;width:{cw}px;height:{ch}px;font-style:inherit;text-align:center;overflow:visible}}
i.w{{width:{2 * cw}px}}
</style><body>{"".join(body)}"""
    path = os.path.join(dst, f.removesuffix(".ans") + ".html")
    with open(path, "w", encoding="utf-8") as o:
        o.write(page)
    print(path, f.removesuffix(".ans") + ".png", w * cw, h * ch)
PY

rm -f "$out"/sheet-*.png
find "$out" -maxdepth 1 -name '*-*x*-*.png' -delete

par=$(( $(nproc) / 2 ))
[ "$par" -ge 1 ] || par=1
[ "$par" -le 8 ] || par=8
n=0
while read -r page png w h; do
	(
		profile=$(mktemp -d -p "$work")
		"$chrome" --headless --disable-gpu --hide-scrollbars --no-first-run --no-default-browser-check \
			--user-data-dir="$profile" --window-size="$w,$h" --screenshot="$out/$png" "file://$page" \
			>"$profile.log" 2>&1 || { cat "$profile.log" >&2; exit 1; }
		[ -s "$out/$png" ] || { echo "$0: no screenshot for $png" >&2; cat "$profile.log" >&2; exit 1; }
	) &
	n=$((n + 1))
	if [ "$n" -ge "$par" ]; then
		wait -n
		n=$((n - 1))
	fi
done <"$work/list"
while [ "$n" -gt 0 ]; do
	wait -n
	n=$((n - 1))
done

font=$(fc-match -f '%{file}' sans:bold)
cut -d' ' -f2 "$work/list" | sed -E 's/^([a-z]+-[0-9]+x[0-9]+)-.*/\1/' | sort -u >"$work/sizes"
while read -r size; do
	args=()
	while read -r _ png _ _; do
		case $png in
		"$size"-*)
			name=${png#"$size"-}
			args+=(-label "${name%.png}" "$out/$png")
			;;
		esac
	done <"$work/list"
	magick montage -font "$font" -pointsize 18 -fill '#e0e0e0' -background '#2b2b2f' \
		"${args[@]}" -tile 4x -geometry +12+12 "$out/sheet-$size.png"
	echo "$out/sheet-$size.png: $(( ${#args[@]} / 3 )) screens"
done <"$work/sizes"
