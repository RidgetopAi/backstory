import QtQuick

// Minimal stand-in for Quickshell's IpcHandler — the host calls its
// functions over `omarchy-shell shell summon/toggle/hide`; this stub only
// needs to be instantiable so Panel.qml's own open()/close()/toggle()/
// refresh()/ping() function declarations compile (see Quickshell.qml's own
// doc comment for why this stays minimal). A QML test drives those
// functions directly (IpcHandler itself spawns nothing to record).
QtObject {
  property string target: ""
}
