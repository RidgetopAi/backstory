#!/bin/sh
# Backstory install-from-source / uninstall (task 87833ae6). Driven by
# `make install` / `make uninstall`; every location derives from HOME and
# XDG_CONFIG_HOME so a test can aim it at a fixture. It never edits the
# user's hyprland.lua — it installs ops/hyprland/backstory.lua beside it and
# prints the one line to add.
#
#   install.sh install   BUILT_BINARY   (a built backstory binary)
#   install.sh uninstall
set -eu

APP=backstory
PLUGIN_ID=backstory.this-week
CONFIG_HOME="${XDG_CONFIG_HOME:-$HOME/.config}"
BINDIR="${BINDIR:-$HOME/.local/bin}"
BIN="$BINDIR/$APP"
UNIT_DIR="$CONFIG_HOME/systemd/user"
UNIT="$UNIT_DIR/$APP.service"
PLUGINS_DIR="$CONFIG_HOME/omarchy/plugins"
PANEL="$PLUGINS_DIR/$PLUGIN_ID"
PANEL_PREV="$PLUGINS_DIR/.$PLUGIN_ID.prev" # hidden so the shell never loads a second copy
HYPR_DIR="$CONFIG_HOME/hypr"
HYPR_FILE="$HYPR_DIR/$APP.lua"
HYPR_LINE='require("hypr.backstory")'
# The session-locked probe: Omarchy locks with hyprlock.
LOCK_PROC="${BACKSTORY_LOCK_PROC:-hyprlock}"

SRC="$(cd "$(dirname "$0")/.." && pwd)"

is_backstory_panel() {
	[ -f "$1/manifest.json" ] && grep -q "\"id\": *\"$PLUGIN_ID\"" "$1/manifest.json"
}

check_panel_target() {
	if [ -e "$PANEL" ] || [ -L "$PANEL" ]; then
		if [ -L "$PANEL" ] || ! is_backstory_panel "$PANEL"; then
			echo "backstory: refusing to touch $PANEL — it is not a Backstory panel (no manifest id $PLUGIN_ID). Move it aside and re-run make install." >&2
			return 1
		fi
	fi
}

do_install() {
	built="$1"
	[ -x "$built" ] || { echo "backstory: built binary $built not found" >&2; exit 1; }
	check_panel_target || exit 1

	# Binary, keeping the previous one for rollback.
	mkdir -p "$BINDIR"
	if [ -f "$BIN" ]; then
		install -m755 "$BIN" "$BIN.prev"
	fi
	install -m755 "$built" "$BIN.new"
	mv -f "$BIN.new" "$BIN"
	echo "installed $BIN"

	# Unit, then the service: enable --now does not restart a running
	# service, so an upgrade restarts it.
	install -Dm644 "$SRC/ops/backstory.service" "$UNIT"
	systemctl --user daemon-reload
	if systemctl --user is-active --quiet "$APP"; then
		systemctl --user restart "$APP"
		echo "restarted $APP.service"
	else
		systemctl --user enable --now "$APP"
		echo "enabled and started $APP.service"
	fi

	# Hyprland: the file only; the user's hyprland.lua is theirs.
	install -Dm644 "$SRC/ops/hyprland/backstory.lua" "$HYPR_FILE"
	echo "installed $HYPR_FILE"
	echo "To open the panel on its keybind, add this line to your hyprland.lua:"
	echo "    $HYPR_LINE"

	# Panel plugin.
	mkdir -p "$PLUGINS_DIR"
	if [ -d "$PANEL" ]; then
		rm -rf "$PANEL_PREV"
		mv "$PANEL" "$PANEL_PREV"
		echo "previous panel backed up to $PANEL_PREV"
	fi
	cp -a "$SRC/panel" "$PANEL"
	echo "installed $PANEL"

	# New QML types need a shell restart, which must not happen over a
	# locked session.
	if pgrep -x "$LOCK_PROC" >/dev/null 2>&1; then
		echo "session is locked: run \`omarchy restart shell\` to load the new panel"
	else
		omarchy-restart-shell
		echo "restarted the Omarchy shell"
	fi

	# Harness integrations (upgrades its own outdated files). It exits
	# non-zero when no harness is detected, which is not an install failure.
	"$BIN" install || echo "backstory: no agent harness set up (see 'backstory install --help'); continuing"
	"$BIN" version
}

do_uninstall() {
	if [ -f "$UNIT" ] || systemctl --user is-enabled --quiet "$APP" 2>/dev/null; then
		systemctl --user stop "$APP" || true
		systemctl --user disable "$APP" || true
	fi
	rm -f "$UNIT" "$BIN" "$BIN.prev" "$HYPR_FILE"
	systemctl --user daemon-reload || true
	if [ -e "$PANEL" ] || [ -L "$PANEL" ]; then
		if [ -L "$PANEL" ] || ! is_backstory_panel "$PANEL"; then
			echo "backstory: leaving $PANEL — it is not a Backstory panel" >&2
		else
			rm -rf "$PANEL"
		fi
	fi
	rm -rf "$PANEL_PREV"
	echo "uninstalled. Your memory store was not touched."
	echo "Remove $HYPR_LINE from your hyprland.lua, and run \`backstory install <harness> --remove\` for each harness BEFORE uninstalling next time."
}

case "${1:-}" in
install) do_install "${2:-}" ;;
uninstall) do_uninstall ;;
*) echo "usage: install.sh install BINARY | uninstall" >&2; exit 2 ;;
esac
