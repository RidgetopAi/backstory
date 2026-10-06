#!/bin/sh
# Backstory install-from-source / uninstall (task 87833ae6). Driven by
# `make install` / `make uninstall`; every location derives from HOME and
# XDG_CONFIG_HOME so a test can aim it at a fixture. It never edits the
# user's hyprland.lua — it installs ops/hyprland/backstory.lua beside it and
# prints the one line to add.
#
#   install.sh check-go  [GO]        (fail early when Go is older than go.mod's)
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

# Daemon health check after install: poll `backstory status` until it answers,
# each attempt bounded by HEALTH_ATTEMPT_TIMEOUT, the whole wait by
# HEALTH_TIMEOUT (seconds).
HEALTH_TIMEOUT="${BACKSTORY_HEALTH_TIMEOUT:-10}"
HEALTH_ATTEMPT_TIMEOUT="${BACKSTORY_HEALTH_ATTEMPT_TIMEOUT:-2}"

# The installer's own backstory calls must not mint a session or project for
# the build directory (the daemon honours this for `status`).
BACKSTORY_NO_SESSION=1
export BACKSTORY_NO_SESSION

# The panel keybind, as users are told about it.
PANEL_BIND="CTRL+SHIFT+B"

# Go version go.mod needs, e.g. 1.27.
required_go() {
	sed -n 's/^go[[:space:]][[:space:]]*\([0-9][0-9.]*\).*/\1/p' "$SRC/go.mod" | head -n1
}

do_check_go() {
	gobin="${1:-go}"
	need="$(required_go)"
	have=""
	if command -v "$gobin" >/dev/null 2>&1; then
		have="$("$gobin" version 2>/dev/null | sed -n 's/.*go version go\([0-9][0-9.]*\).*/\1/p' | head -n1)"
	fi
	if [ -n "$have" ] && [ "$(printf '%s\n%s\n' "$need" "$have" | sort -V | head -n1)" = "$need" ]; then
		return 0
	fi
	if [ -z "$have" ]; then
		echo "backstory: Go $need or newer is required to build, but no working '$gobin' was found." >&2
	else
		echo "backstory: Go $need or newer is required to build, but '$gobin' is go$have." >&2
	fi
	echo "Fix: mise use -g go@$need   (mise ships with Omarchy), then re-run make install." >&2
	return 1
}

# hyprland_loads_backstory: the user's hyprland.lua already has our require
# line, uncommented. Read only.
hyprland_loads_backstory() {
	[ -f "$HYPR_DIR/hyprland.lua" ] &&
		grep -Eq "^[[:space:]]*require[[:space:]]*\([[:space:]]*[\"']hypr\.backstory[\"'][[:space:]]*\)" "$HYPR_DIR/hyprland.lua"
}

# wait_for_daemon: the unit is active and `backstory status` answers within
# HEALTH_TIMEOUT. Prints the failure line itself.
wait_for_daemon() {
	waited=0
	reason="$APP.service is not active"
	while :; do
		if systemctl --user is-active --quiet "$APP"; then
			if out="$(timeout "$HEALTH_ATTEMPT_TIMEOUT" "$BIN" status 2>&1)"; then
				return 0
			fi
			reason="\`backstory status\` did not answer: $(printf '%s' "$out" | head -n1)"
		fi
		[ "$waited" -ge "$HEALTH_TIMEOUT" ] && break
		sleep 1
		waited=$((waited + 1))
	done
	echo "backstory: the daemon ($APP.service) is not healthy after ${HEALTH_TIMEOUT}s: $reason" >&2
	echo "backstory: see \`systemctl --user status $APP\` and \`journalctl --user -u $APP\`" >&2
	return 1
}

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
	if hyprland_loads_backstory; then
		echo "keybind already loaded from hyprland.lua"
	else
		echo "To open the panel on its keybind, add this line to your hyprland.lua:"
		echo "    $HYPR_LINE"
	fi

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

	# The hook verification below needs a live daemon: wait for it first.
	wait_for_daemon || exit 1

	# Harness integrations (upgrades its own outdated files). It exits
	# non-zero when no harness is detected, which is not an install failure.
	"$BIN" install || echo "backstory: no agent harness set up (see 'backstory install --help'); continuing"

	echo "backstory $("$BIN" version) installed"
	echo "next: open the panel with $PANEL_BIND; agents get memory on their next session."
}

# unregister_harnesses: take Backstory out of every harness (and bashrc) BEFORE
# the binary goes, or their hooks keep calling a binary that no longer exists.
# `install --remove` with no harness named acts on each detected harness. A
# failure is reported but does not stop the uninstall.
unregister_harnesses() {
	[ -x "$BIN" ] || return 0
	"$BIN" install --remove || echo "backstory: could not remove every harness integration; check ~/.claude, ~/.codex, ~/.pi and ~/.hermes" >&2
	"$BIN" install bash --remove || echo "backstory: could not remove the ~/.bashrc block; remove the backstory:begin..end block by hand" >&2
}

do_uninstall() {
	unregister_harnesses
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
	echo "Harness integrations and the ~/.bashrc block were removed first. Remove $HYPR_LINE from your hyprland.lua."
}

case "${1:-}" in
check-go) do_check_go "${2:-go}" ;;
install) do_install "${2:-}" ;;
uninstall) do_uninstall ;;
*) echo "usage: install.sh check-go [GO] | install BINARY | uninstall" >&2; exit 2 ;;
esac
