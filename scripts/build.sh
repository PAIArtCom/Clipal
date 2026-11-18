#!/bin/bash

# Cross-platform build script for clipal

set -e

# Get version info
VERSION=${VERSION:-"dev"}
COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

# Build output directory
BUILD_DIR="build"
rm -rf "$BUILD_DIR"
mkdir -p "$BUILD_DIR"

# Linker flags for version info
LDFLAGS="-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.date=$DATE"

# Build targets
TARGETS=(
    "darwin:amd64"
    "darwin:arm64"
    "linux:amd64"
    "linux:arm64"
    "windows:amd64"
    "windows:arm64"
)

echo "Building clipal $VERSION ($COMMIT)"
echo "================================"

for target in "${TARGETS[@]}"; do
    IFS=':' read -r os arch <<< "$target"

    output_dir="$BUILD_DIR/${os}-${arch}"
    mkdir -p "$output_dir"

    output_name="clipal"
    if [ "$os" = "windows" ]; then
        output_name="clipal.exe"
    fi

    echo "Building for $os/$arch..."

    GOOS=$os GOARCH=$arch go build \
        -trimpath \
        -ldflags "$LDFLAGS" \
        -o "$output_dir/$output_name" \
        ./cmd/clipal

    echo "  -> $output_dir/$output_name"
done

echo ""
echo "Build complete!"
echo ""
echo "Artifacts:"
find "$BUILD_DIR" -type f -name "clipal*" | sort
