#!/bin/bash
set -euo pipefail

project_root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$project_root"

if [[ "$(uname -s)" != Darwin ]]; then
    echo 'macOS 和 Xcode Command Line Tools 是构建桌面程序的必要条件。' >&2
    exit 1
fi

download=false
if [[ "${1:-}" == --download-dependencies ]]; then
    download=true
elif [[ $# -ne 0 ]]; then
    echo '用法：GO=/path/to/go scripts/build-macos.sh [--download-dependencies]' >&2
    exit 1
fi

go_command="${GO:-go}"
command -v "$go_command" >/dev/null
xcrun --find clang >/dev/null
unset GOROOT
export GOTOOLCHAIN=local CGO_ENABLED=1 GOOS=darwin
export GOMODCACHE="$project_root/.tmp/go-mod"
export GOCACHE="$project_root/.tmp/go-build"
export GOTMPDIR="$project_root/.tmp/go-tmp"
export MACOSX_DEPLOYMENT_TARGET=11.0
mkdir -p "$GOMODCACHE" "$GOCACHE" "$GOTMPDIR"

if $download; then
    "$go_command" mod download
else
    export GOPROXY=off GOSUMDB=off
fi

app="$project_root/dist/ime-pilot.app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp "$project_root/assets/icons/macos/ime-pilot.icns" "$app/Contents/Resources/ime-pilot.icns"
"$go_command" build -mod=readonly -tags production -trimpath -o "$app/Contents/MacOS/ime-pilot" ./cmd/ime-pilot
cat > "$app/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
    <key>CFBundleExecutable</key><string>ime-pilot</string>
    <key>CFBundleIdentifier</key><string>local.imepilot.desktop</string>
    <key>CFBundleName</key><string>IME Pilot</string>
    <key>CFBundleDisplayName</key><string>IME Pilot</string>
    <key>CFBundleIconFile</key><string>ime-pilot.icns</string>
    <key>CFBundlePackageType</key><string>APPL</string>
    <key>CFBundleShortVersionString</key><string>0.1.0</string>
    <key>CFBundleVersion</key><string>1</string>
    <key>LSMinimumSystemVersion</key><string>11.0</string>
    <key>LSUIElement</key><true/>
    <key>NSHighResolutionCapable</key><true/>
</dict></plist>
PLIST
plutil -lint "$app/Contents/Info.plist"
codesign --force --sign - "$app"
echo "已构建：$app"
echo '未启动程序，也未修改输入源；可用 open dist/ime-pilot.app 启动。'
