#!/usr/bin/env bash
# package.sh builds the desktop app for one target and packs it into
# dist-release/. CI runs it on every pull request; the release workflow runs
# it on a tag. Build the UI first (make ui-build); it is embedded.
#
#   VERSION=v0.0.1 scripts/package.sh linux-amd64
#   VERSION=v0.0.1 scripts/package.sh macos-universal   # on macOS
#   VERSION=v0.0.1 scripts/package.sh windows-amd64     # on Windows (Git Bash)
set -euo pipefail

target=${1:?usage: VERSION=vX.Y.Z scripts/package.sh <linux-amd64|macos-universal|windows-amd64>}
version=${VERSION:?set VERSION, e.g. VERSION=v0.0.1}

cd "$(dirname "$0")/.."
if [[ ! -f ui/shell/dist/index.html ]]; then
	echo "package.sh: the UI is not built; run make ui-build first" >&2
	exit 1
fi

out=dist-release
work=build/pkg
name="lumen-$version-$target"
ldflags="-s -w -X main.version=$version"
rm -rf "$work" && mkdir -p "$out" "$work"

build() { go build -tags production -trimpath "$@" ./cmd/lumen; }

case $target in
linux-amd64)
	mkdir -p "$work/$name"
	GOOS=linux GOARCH=amd64 build -ldflags "$ldflags" -o "$work/$name/lumen"
	cp LICENSE "$work/$name/"
	cp assets/icon/lumen-256.png "$work/$name/lumen.png"
	cp assets/linux/io.github.phankieuphu.lumen.desktop assets/linux/install.sh "$work/$name/"
	tar -C "$work" -czf "$out/$name.tar.gz" "$name"
	;;

macos-universal)
	# One binary per architecture, joined with lipo. Unsigned: signing and
	# notarization are phase 6 work.
	export CGO_ENABLED=1 MACOSX_DEPLOYMENT_TARGET=11.0
	CC="clang -arch arm64" GOOS=darwin GOARCH=arm64 build -ldflags "$ldflags" -o "$work/lumen-arm64"
	CC="clang -arch x86_64" GOOS=darwin GOARCH=amd64 build -ldflags "$ldflags" -o "$work/lumen-amd64"
	app="$work/lumen.app"
	mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
	cp assets/icon/lumen.icns "$app/Contents/Resources/lumen.icns"
	lipo -create -output "$app/Contents/MacOS/lumen" "$work/lumen-arm64" "$work/lumen-amd64"
	cat >"$app/Contents/Info.plist" <<EOF
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
EOF
	# Ad-hoc signature: required to launch on Apple silicon at all.
	codesign --force --deep --sign - "$app"
	ditto -c -k --keepParent "$app" "$out/$name.zip"
	;;

windows-amd64)
	mkdir -p "$work/$name"
	# The icon Explorer shows for lumen.exe has to be a resource linked into
	# the binary: go-winres writes it as a .syso that go build picks up. The
	# gui manifest also marks the app DPI-aware. Removed after the build so it
	# never lands in the tree.
	syso=cmd/lumen/rsrc_windows_amd64.syso
	trap 'rm -f "$syso"' EXIT
	# Windows version fields are numeric, so only a vX.Y.Z tag is passed on;
	# CI builds (VERSION=ci-<sha>) leave them at 0.0.0.0.
	winver=()
	if [[ $version =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
		winver=(--product-version "${version#v}" --file-version "${version#v}")
	fi
	go run github.com/tc-hib/go-winres@v0.3.3 simply \
		--arch amd64 --out cmd/lumen/rsrc --manifest gui \
		--icon assets/icon/lumen.ico \
		--product-name lumen --file-description "lumen SQL client" \
		"${winver[@]}"
	GOOS=windows GOARCH=amd64 build -ldflags "$ldflags -H windowsgui" -o "$work/$name/lumen.exe"
	cp LICENSE "$work/$name/LICENSE.txt"
	(cd "$work" && 7z a -tzip "../../$out/$name.zip" "$name" >/dev/null)
	;;

*)
	echo "package.sh: unknown target $target" >&2
	exit 1
	;;
esac

echo "package.sh: wrote $out/$name.*"
