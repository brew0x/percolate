#!/usr/bin/env bash
set -euo pipefail

# Generates demo/percolate.gif from a recorded fixture, and records the exact
# text frames it encodes in demo/frames.sha256. Requires: go, ffmpeg, sha256sum,
# and a monospace font with the glyphs Percolate draws.
#
# GIF bytes depend on the ffmpeg build and font rasterizer, so the manifest —
# not the encoded GIF — is the reproducible proof. make check replays the same
# frames from the fixture and compares their hashes, then checks that the GIF's
# frame sequence follows them.

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

fixture="testdata/fixtures/small.json"
size="80x24"
out="demo/percolate.gif"
manifest="demo/frames.sha256"
font="${DEMO_FONT:-}"
if [ -z "$font" ] && command -v fc-match >/dev/null 2>&1; then
  font="$(fc-match -f '%{file}' 'Liberation Mono:style=Regular' 2>/dev/null || true)"
  if [ -z "$font" ]; then
    font="$(fc-match -f '%{file}' 'DejaVu Sans Mono:style=Book' 2>/dev/null || true)"
  fi
fi
for candidate in \
  "$font" \
  /usr/share/fonts/liberation/LiberationMono-Regular.ttf \
  /usr/share/fonts/truetype/liberation/LiberationMono-Regular.ttf \
  /usr/share/fonts/truetype/liberation2/LiberationMono-Regular.ttf \
  /usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf \
  /usr/share/fonts/dejavu-sans-mono-fonts/DejaVuSansMono.ttf \
  /usr/share/fonts/noto/NotoSansMono-Regular.ttf \
  /usr/share/fonts/Adwaita/AdwaitaMono-Regular.ttf \
  /usr/share/fonts/TTF/JetBrainsMonoNerdFont-Regular.ttf; do
  if [ -n "$candidate" ] && [ -f "$candidate" ]; then
    font="$candidate"
    break
  fi
done
if [ -z "$font" ] || [ ! -f "$font" ]; then
  echo "no monospace font found for demo generation" >&2
  exit 1
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

bin="$tmp/percolate"
go build -o "$bin" ./cmd/percolate

# One full pulse cycle (32 frames) shows recorded running CI in motion.
: > "$tmp/frames.sha256"
for i in $(seq 0 31); do
  frame=$(printf '%02d' "$i")
  "$bin" render --fixture "$fixture" --frame "$i" --size "$size" --color=false > "$tmp/frame.txt"
  digest="$(sha256sum "$tmp/frame.txt" | cut -d' ' -f1)"
  printf '%s  %s\n' "$frame" "$digest" >> "$tmp/frames.sha256"
  ffmpeg -hide_banner -loglevel error -y \
    -f lavfi -i "color=c=0x0f1419:s=900x560" \
    -vf "drawtext=fontfile='${font}':textfile='${tmp}/frame.txt':fontcolor=0xd5ddd9:fontsize=16:x=24:y=24:line_spacing=4,format=rgb24" \
    -frames:v 1 "$tmp/frame_${frame}.png"
done

# Two-pass palette for a small, deterministic GIF.
ffmpeg -hide_banner -loglevel error -y -framerate 8 -i "$tmp/frame_%02d.png" \
  -vf "palettegen=stats_mode=diff" "$tmp/palette.png"
ffmpeg -hide_banner -loglevel error -y -framerate 8 -i "$tmp/frame_%02d.png" -i "$tmp/palette.png" \
  -lavfi "paletteuse=dither=bayer:bayer_scale=3" -loop 0 "$out"

cp "$tmp/frames.sha256" "$manifest"
echo "wrote $out and $manifest"
