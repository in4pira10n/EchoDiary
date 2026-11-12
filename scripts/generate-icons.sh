#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
SOURCE="$ROOT/assets/icons/EchoDiary.png"
WEB_ASSETS="$ROOT/web/static/assets"
DESKTOP_ASSETS="$ROOT/assets/icons"

command -v python3 >/dev/null
mkdir -p "$WEB_ASSETS" "$DESKTOP_ASSETS"

python3 - "$SOURCE" "$WEB_ASSETS" "$DESKTOP_ASSETS" <<'PY'
from pathlib import Path
import sys

from PIL import Image

source_path, web_dir, desktop_dir = map(Path, sys.argv[1:])
source = Image.open(source_path).convert("RGBA")

web_sizes = {
    "echodiary-icon.png": 1024,
    "pwa-icon-512.png": 512,
    "pwa-icon-192.png": 192,
    "apple-touch-icon.png": 180,
    "favicon-32.png": 32,
    "favicon-16.png": 16,
}
for filename, size in web_sizes.items():
    source.resize((size, size), Image.Resampling.LANCZOS).save(
        web_dir / filename, format="PNG", optimize=True
    )

source.resize((512, 512), Image.Resampling.LANCZOS).save(
    web_dir / "favicon.ico",
    format="ICO",
    sizes=[(16, 16), (24, 24), (32, 32), (48, 48), (64, 64), (128, 128), (256, 256)],
)
(desktop_dir / "EchoDiary.ico").write_bytes((web_dir / "favicon.ico").read_bytes())
PY

if [ "$(uname -s)" = "Darwin" ] && command -v iconutil >/dev/null 2>&1; then
  ICONSET_ROOT=$(mktemp -d)
  trap 'rm -rf "$ICONSET_ROOT"' EXIT
  ICONSET="$ICONSET_ROOT/EchoDiary.iconset"
  mkdir -p "$ICONSET"
  python3 - "$SOURCE" "$ICONSET" <<'PY'
from pathlib import Path
import sys

from PIL import Image

source = Image.open(sys.argv[1]).convert("RGBA")
iconset = Path(sys.argv[2])
variants = {
    "icon_16x16.png": 16,
    "icon_16x16@2x.png": 32,
    "icon_32x32.png": 32,
    "icon_32x32@2x.png": 64,
    "icon_128x128.png": 128,
    "icon_128x128@2x.png": 256,
    "icon_256x256.png": 256,
    "icon_256x256@2x.png": 512,
    "icon_512x512.png": 512,
    "icon_512x512@2x.png": 1024,
}
for filename, size in variants.items():
    source.resize((size, size), Image.Resampling.LANCZOS).save(iconset / filename)
PY
  iconutil --convert icns --output "$DESKTOP_ASSETS/EchoDiary.icns" "$ICONSET"
else
  printf '%s\n' 'Skipping EchoDiary.icns: macOS iconutil is unavailable.' >&2
fi

# The resource is optional for non-Windows development, but it lets Go embed
# the same icon into Windows amd64 executables.
if command -v go >/dev/null 2>&1; then
  go run github.com/akavel/rsrc@v0.10.2 \
    -ico "$WEB_ASSETS/favicon.ico" -arch amd64 -o "$ROOT/rsrc_windows_amd64.syso"
else
  printf '%s\n' 'Skipping Windows executable resource: Go is unavailable.' >&2
fi
