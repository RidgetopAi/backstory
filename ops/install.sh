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
SYNC_SERVICE="$UNIT_DIR/$APP-sync.service"
SYNC_TIMER="$UNIT_DIR/$APP-sync.timer"
PLUGINS_DIR="$CONFIG_HOME/omarchy/plugins"
PANEL="$PLUGINS_DIR/$PLUGIN_ID"
PANEL_PREV="$PLUGINS_DIR/.$PLUGIN_ID.prev" # hidden so the shell never loads a second copy
HYPR_DIR="$CONFIG_HOME/hypr"
HYPR_FILE="$HYPR_DIR/$APP.lua"
HYPR_LINE='require("hypr.backstory")'
# The session-locked probe: Omarchy's own omarchy-hyprland-session-locked when
# it is on PATH (4.x locks inside the shell, with no hyprlock process); older
# Omarchy locks with hyprlock, which is looked for by process name.
LOCK_PROC="${BACKSTORY_LOCK_PROC:-hyprlock}"
# Where Hyprland keeps one directory per running instance.
HYPR_RUN_DIR="${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/hypr"

# A shell restart refused because the session is locked is deferred to a
# transient user unit that polls every RESTART_POLL seconds and gives up after
# RESTART_GIVEUP seconds (24h). A second install replaces the pending unit.
RESTART_UNIT="${BACKSTORY_RESTART_UNIT:-backstory-shell-restart}"
RESTART_POLL="${BACKSTORY_RESTART_POLL:-5}"
RESTART_GIVEUP="${BACKSTORY_RESTART_GIVEUP:-86400}"

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

# The Omarchy CLI, and the exact command that turns the panel on: copying the
# plugin directory does not enable it.
OMARCHY_CLI="${BACKSTORY_OMARCHY_CLI:-omarchy}"
ENABLE_CMD="$OMARCHY_CLI plugin enable $PLUGIN_ID"
DISABLE_CMD="$OMARCHY_CLI plugin disable $PLUGIN_ID"

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

# hypr_signature HYPR_DIR: print the live Hyprland instance. An ssh session has
# no HYPRLAND_INSTANCE_SIGNATURE, so omarchy-hyprland-session-locked cannot
# reach hyprctl and answers "undetermined"; derive the variable the way
# omarchy-restart-shell does: the newest directory under HYPR_DIR.
hypr_signature() {
	if [ -n "${HYPRLAND_INSTANCE_SIGNATURE:-}" ]; then
		printf '%s\n' "$HYPRLAND_INSTANCE_SIGNATURE"
		return 0
	fi
	# shellcheck disable=SC2012  # directory names are Hyprland instance signatures
	newest="$(ls -t "$1" 2>/dev/null | head -n1)"
	[ -n "$newest" ] && printf '%s\n' "$newest"
	return 0
}

# reload_hyprland: apply the newly installed backstory.lua at once and check
# it, as Omarchy's own Hyprland changes do (its agent skill, hyprland.md:
# `hyprctl reload`, then `hyprctl configerrors`). A config error is a warning,
# not a failure: the daemon and the panel installed fine. Skipped when no
# Hyprland can be reached.
reload_hyprland() {
	command -v hyprctl >/dev/null 2>&1 || return 0
	sig="$(hypr_signature "$HYPR_RUN_DIR")"
	[ -n "$sig" ] || return 0
	if ! HYPRLAND_INSTANCE_SIGNATURE="$sig" hyprctl reload >/dev/null 2>&1; then
		echo "warning: could not reload Hyprland; the keybind applies at its next reload"
		return 0
	fi
	if ! errors="$(HYPRLAND_INSTANCE_SIGNATURE="$sig" hyprctl configerrors 2>/dev/null)"; then
		echo "warning: reloaded Hyprland but could not check its config"
		return 0
	fi
	if [ -n "$(printf '%s' "$errors" | tr -d '[:space:]')" ]; then
		echo "warning: Hyprland reports config errors after reloading:"
		printf '%s\n' "$errors"
	else
		echo "reloaded Hyprland"
	fi
}

# session_locked LOCK_PROC: the shell must NOT be restarted now. Three-state
# with Omarchy's own probe: exit 1 = definitely unlocked (restart now); 0 =
# locked and 2 = undetermined both defer. Without the probe, a process by that
# name (found = locked).
session_locked() {
	if command -v omarchy-hyprland-session-locked >/dev/null 2>&1; then
		sig="$(hypr_signature "$HYPR_RUN_DIR")"
		HYPRLAND_INSTANCE_SIGNATURE="$sig" omarchy-hyprland-session-locked >/dev/null 2>&1
		[ "$?" -ne 1 ]
	else
		pgrep -x "$1" >/dev/null 2>&1
	fi
}

# The deferred restart, run by the transient unit as: sh -c SCRIPT sh POLL
# GIVEUP LOCK_PROC HYPR_RUN_DIR. Same probe as session_locked (kept in step by
# hand: the unit has no access to this file); the signature is re-derived on
# every poll because Hyprland can restart while waiting.
# shellcheck disable=SC2016  # expanded by the unit's own shell
DEFERRED_RESTART='
poll="$1"; giveup="$2"; proc="$3"; hyprdir="$4"; waited=0
locked() {
	if command -v omarchy-hyprland-session-locked >/dev/null 2>&1; then
		sig="${HYPRLAND_INSTANCE_SIGNATURE:-}"
		[ -n "$sig" ] || sig="$(ls -t "$hyprdir" 2>/dev/null | head -n1)"
		HYPRLAND_INSTANCE_SIGNATURE="$sig" omarchy-hyprland-session-locked >/dev/null 2>&1
		[ "$?" -ne 1 ]
	else
		pgrep -x "$proc" >/dev/null 2>&1
	fi
}
while locked; do
	[ "$waited" -ge "$giveup" ] && exit 0
	sleep "$poll"
	waited=$((waited + poll))
done
omarchy-restart-shell
'

# defer_shell_restart: start (replacing any pending one) the unit that
# restarts the shell once the session unlocks. Returns non-zero when it cannot.
defer_shell_restart() {
	command -v systemd-run >/dev/null 2>&1 || return 1
	systemctl --user stop "$RESTART_UNIT.service" >/dev/null 2>&1 || true
	systemctl --user reset-failed "$RESTART_UNIT.service" >/dev/null 2>&1 || true
	systemd-run --user --quiet --unit="$RESTART_UNIT" --collect \
		--setenv=PATH="$PATH" \
		sh -c "$DEFERRED_RESTART" sh "$RESTART_POLL" "$RESTART_GIVEUP" "$LOCK_PROC" "$HYPR_RUN_DIR"
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

# plugin_toggle ENABLE|DISABLE: run the Omarchy plugin command. A missing CLI
# or a failing command never fails the install; it prints the command to run.
plugin_toggle() {
	if [ "$1" = enable ]; then cmd="$ENABLE_CMD"; else cmd="$DISABLE_CMD"; fi
	if ! command -v "$OMARCHY_CLI" >/dev/null 2>&1; then
		echo "backstory: the '$OMARCHY_CLI' CLI is not on PATH; run this yourself: $cmd"
	elif $cmd; then
		echo "plugin $1d: $PLUGIN_ID"
	else
		echo "backstory: \`$cmd\` failed; run it yourself"
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
		reload_hyprland
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
	# Runtime files only: the manifest, the QML types and their JS. The
	# panel's Go tests, testdata and qmltest/ stay in the source tree.
	mkdir -p "$PANEL"
	cp -a "$SRC/panel/manifest.json" "$SRC"/panel/*.qml "$SRC/panel/js" "$PANEL/"
	echo "installed $PANEL"
	plugin_toggle enable

	# New QML types need a shell restart, which must never happen over a
	# locked session (it would kill the lock screen) and is never fatal.
	if session_locked "$LOCK_PROC"; then
		if defer_shell_restart; then
			echo "session is locked: the panel loads when you unlock (or run \`omarchy restart shell\`)"
		else
			echo "session is locked: run \`omarchy restart shell\` to load the new panel"
		fi
	elif omarchy-restart-shell; then
		echo "restarted the Omarchy shell"
	else
		echo "backstory: the shell restart did not complete; run \`omarchy restart shell\` to load the new panel"
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
	# The optional sync timer (`backstory install sync`), when present.
	if [ -f "$SYNC_TIMER" ] || [ -f "$SYNC_SERVICE" ]; then
		systemctl --user disable --now "$APP-sync.timer" || true
		rm -f "$SYNC_TIMER" "$SYNC_SERVICE"
	fi
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
			plugin_toggle disable
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
