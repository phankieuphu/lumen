#!/usr/bin/env bash
# macos-bundle.sh wraps a lumen binary in a macOS .app bundle. macOS only
# treats a process as a full app (its own fullscreen Space, ⌘Tab, Dock icon,
# bundle id) when it runs from a bundle; a bare binary launched from a
# terminal can stay on top of other apps in fullscreen.
#
#   scripts/macos-bundle.sh <binary> <app dir> [version]
set -euo pipefail

bin=${1:?usage: scripts/macos-bundle.sh <binary> <app dir> [version]}
app=${2:?usage: scripts/macos-bundle.sh <binary> <app dir> [version]}
version=${3:-dev}

cd "$(dirname "$0")/.."
rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp assets/icon/lumen.icns "$app/Contents/Resources/lumen.icns"
cp "$bin" "$app/Contents/MacOS/lumen"
cat >"$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key><string>lumen</string>
	<key>CFBundleDisplayName</key><string>lumen</string>
	<key>CFBundleIdentifier</key><string>io.github.phankieuphu.lumen</string>
	<key>CFBundleExecutable</key><string>lumen</string>
	<key>CFBundleIconFile</key><string>lumen</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>${version#v}</string>
	<key>CFBundleVersion</key><string>${version#v}</string>
	<key>LSMinimumSystemVersion</key><string>11.0</string>
	<key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST
# Ad-hoc signature: required to launch on Apple silicon at all.
codesign --force --deep --sign - "$app"
