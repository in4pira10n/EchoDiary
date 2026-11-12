#!/bin/sh

# EchoDiary multi-platform build script, following EchoGallery's build interface.
set -eu

RED=$(printf '\033[0;31m')
GREEN=$(printf '\033[0;32m')
YELLOW=$(printf '\033[1;33m')
NC=$(printf '\033[0m')
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$SCRIPT_DIR"

PROJECT_NAME=EchoDiary
TARGET_PLATFORM=all
TARGET_ARCH=all
VERSION=
PACKAGE_MODE=false
CLEAN_MODE=false
BUILD_ROOT="$SCRIPT_DIR/.build"
DEFAULT_OUTPUT_DIR="$HOME/Downloads"
WINDOWS_AMD_OUTPUT_DIR=/Volumes/Users/Public/Downloads
LINUX_AMD_OUTPUT_DIR=/Volumes/share

usage() {
    printf '%s\n' "Usage: sh build.sh [platform] [arch] [version] [--clean] [--package]"
    printf '%s\n' "platform: all | windows | macos | darwin | linux"
    printf '%s\n' "arch: all | amd | amd64 | x64 | intel | arm | arm64 | aarch64"
    printf '%s\n' "Examples: sh build.sh windows"
    printf '%s\n' "          sh build.sh linux amd --package"
    printf '%s\n' "          sh build.sh macos arm64 v1.2.0 --package"
}

is_version() {
    case "$1" in
        v[0-9]*.[0-9]*.[0-9]*) return 0 ;;
        *) return 1 ;;
    esac
}

normalize_platform() {
    case "$1" in
        all) printf '%s\n' all ;;
        windows|win) printf '%s\n' windows ;;
        macos|mac|darwin) printf '%s\n' macos ;;
        linux) printf '%s\n' linux ;;
        *) return 1 ;;
    esac
}

normalize_arch() {
    case "$1" in
        all) printf '%s\n' all ;;
        amd|amd64|x64|intel) printf '%s\n' amd64 ;;
        arm|arm64|aarch64) printf '%s\n' arm64 ;;
        *) return 1 ;;
    esac
}

# Match EchoGallery's package destinations; staging stays inside the project.
resolve_output_dir() {
    if [ "$1" = windows ] && [ "$2" = amd64 ] && [ -d "$WINDOWS_AMD_OUTPUT_DIR" ] && [ -w "$WINDOWS_AMD_OUTPUT_DIR" ]; then
        printf '%s\n' "$WINDOWS_AMD_OUTPUT_DIR"
    elif [ "$1" = linux ] && [ "$2" = amd64 ] && [ -d "$LINUX_AMD_OUTPUT_DIR" ] && [ -w "$LINUX_AMD_OUTPUT_DIR" ]; then
        printf '%s\n' "$LINUX_AMD_OUTPUT_DIR"
    else
        printf '%s\n' "$DEFAULT_OUTPUT_DIR"
    fi
}

for ARG in "$@"; do
    case "$ARG" in
        --package) PACKAGE_MODE=true ;;
        --clean) CLEAN_MODE=true ;;
        -h|--help) usage; exit 0 ;;
        *)
            if NORMALIZED=$(normalize_platform "$ARG" 2>/dev/null); then
                TARGET_PLATFORM=$NORMALIZED
            elif NORMALIZED=$(normalize_arch "$ARG" 2>/dev/null); then
                TARGET_ARCH=$NORMALIZED
            elif is_version "$ARG"; then
                VERSION=$ARG
            else
                printf '%sError: unrecognized argument %s%s\n' "$RED" "$ARG" "$NC" >&2
                usage >&2
                exit 1
            fi
            ;;
    esac
done

if [ -z "$VERSION" ]; then
    VERSION=$(git describe --tags --abbrev=0 2>/dev/null || printf '%s\n' v0.0.1)
    printf '%sNo version supplied; using %s%s\n' "$YELLOW" "$VERSION" "$NC"
fi

PLATFORMS=$(printf '%s\n' \
    darwin:amd64:macos:amd64 \
    darwin:arm64:macos:arm64 \
    windows:amd64:windows:amd64 \
    windows:arm64:windows:arm64 \
    linux:amd64:linux:amd64 \
    linux:arm64:linux:arm64)
TOTAL=$(printf '%s\n' "$PLATFORMS" | awk -F: -v target="$TARGET_PLATFORM" -v arch="$TARGET_ARCH" \
    '(target == "all" || $3 == target) && (arch == "all" || $4 == arch) { count++ } END { print count + 0 }')
if [ "$TOTAL" -eq 0 ]; then
    printf '%sError: no build targets match %s / %s%s\n' "$RED" "$TARGET_PLATFORM" "$TARGET_ARCH" "$NC" >&2
    exit 1
fi

if [ "$CLEAN_MODE" = true ]; then
    rm -rf "$BUILD_ROOT"
fi
mkdir -p "$BUILD_ROOT"

printf '%sEchoDiary build%s\n' "$GREEN" "$NC"
printf 'Version: %s\nPlatform: %s\nArchitecture: %s\n' "$VERSION" "$TARGET_PLATFORM" "$TARGET_ARCH"

SUCCESS=0
FAILED=0
while IFS=: read -r GOOS GOARCH PLATFORM_NAME ARCH_NAME; do
    [ "$TARGET_PLATFORM" = all ] || [ "$PLATFORM_NAME" = "$TARGET_PLATFORM" ] || continue
    [ "$TARGET_ARCH" = all ] || [ "$ARCH_NAME" = "$TARGET_ARCH" ] || continue

    BUILD_DIR="$BUILD_ROOT/$PLATFORM_NAME-$ARCH_NAME"
    BINARY_NAME=$PROJECT_NAME
    if [ "$GOOS" = windows ]; then BINARY_NAME="${PROJECT_NAME}.exe"; fi
    BINARY_PATH="$BUILD_DIR/$BINARY_NAME"
    PACKAGE_BASENAME="${PROJECT_NAME}_${PLATFORM_NAME}_${ARCH_NAME}_${VERSION}"
    mkdir -p "$BUILD_DIR"

    printf '%sBuilding%s %s %s ... ' "$YELLOW" "$NC" "$PLATFORM_NAME" "$ARCH_NAME"
    if GOOS="$GOOS" GOARCH="$GOARCH" go build -trimpath -o "$BINARY_PATH" .; then
        printf '%sOK%s\n' "$GREEN" "$NC"
        if [ "$PACKAGE_MODE" = true ]; then
            OUTPUT_DIR=$(resolve_output_dir "$GOOS" "$GOARCH")
            if [ "$GOOS" = windows ]; then
                ARCHIVE_PATH="$OUTPUT_DIR/${PACKAGE_BASENAME}.zip"
            else
                ARCHIVE_PATH="$OUTPUT_DIR/${PACKAGE_BASENAME}.tar.gz"
            fi
            # Publish a complete, fresh archive instead of updating an old ZIP.
            if (
                set -e
                mkdir -p "$OUTPUT_DIR" || exit 1
                TEMP_ARCHIVE="$OUTPUT_DIR/.${PACKAGE_BASENAME}.$$.tmp.zip"
                trap 'rm -f "$TEMP_ARCHIVE"' 0
                if [ "$GOOS" = windows ]; then
                    (cd "$BUILD_DIR" && zip -q "$TEMP_ARCHIVE" "$BINARY_NAME") || exit 1
                else
                    tar -C "$BUILD_DIR" -czf "$TEMP_ARCHIVE" "$BINARY_NAME" || exit 1
                fi
                mv -f "$TEMP_ARCHIVE" "$ARCHIVE_PATH"
            ); then
                printf 'Package: %s\n' "$ARCHIVE_PATH"
            else
                printf '%sPackaging FAILED: %s%s\n' "$RED" "$ARCHIVE_PATH" "$NC" >&2
                FAILED=$((FAILED + 1))
                continue
            fi
        fi
        SUCCESS=$((SUCCESS + 1))
    else
        printf '%sFAILED%s\n' "$RED" "$NC"
        FAILED=$((FAILED + 1))
    fi
done <<EOF
$PLATFORMS
EOF

printf '\nBuild complete: %s/%s succeeded' "$SUCCESS" "$TOTAL"
[ "$FAILED" -eq 0 ] || printf ', %s failed' "$FAILED"
printf '\nBuild outputs: %s\n' "$BUILD_ROOT"
[ "$FAILED" -eq 0 ]
