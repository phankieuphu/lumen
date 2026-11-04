#!/bin/sh
# Installs lumen for the current user: the binary in ~/.local/bin, plus a
# menu entry and icon so the desktop shows lumen with its logo. No sudo.
#   ./install.sh            install or update
#   ./install.sh --remove   uninstall (your data in ~/.config/lumen is kept)
set -eu
here=$(cd "$(dirname "$0")" && pwd)
id=io.github.phankieuphu.lumen
bin="$HOME/.local/bin"
apps="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
icons="${XDG_DATA_HOME:-$HOME/.local/share}/icons/hicolor/256x256/apps"

if [ "${1:-}" = "--remove" ]; then
	rm -f "$bin/lumen" "$apps/$id.desktop" "$icons/$id.png"
	echo "lumen removed. Your data in ~/.config/lumen is kept."
	exit 0
fi

mkdir -p "$bin" "$apps" "$icons"
install -m 755 "$here/lumen" "$bin/lumen"
install -m 644 "$here/lumen.png" "$icons/$id.png"
sed "s|^Exec=.*|Exec=$bin/lumen|" "$here/$id.desktop" >"$apps/$id.desktop"
command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database "$apps" >/dev/null 2>&1 || true
command -v gtk-update-icon-cache >/dev/null 2>&1 && gtk-update-icon-cache -q "$(dirname "$(dirname "$icons")")" >/dev/null 2>&1 || true
echo "lumen installed: open it from your app menu, or run $bin/lumen"
