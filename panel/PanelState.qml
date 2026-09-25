pragma Singleton
import QtQuick

// PanelState is the one piece of state shared between this plugin's two
// entry points (Panel.qml's "panel" kind and BarWidget.qml's "bar-widget"
// kind, manifest.json — round 2 defect B, "nothing on screen opens the
// panel"). It holds nothing this plugin reads from `backstory this-week
// --json` or spawns a process over, just whether the panel is open, so
// BarWidget's click can toggle Panel's visibility without either entry
// point reaching into the other's tree or a third process being spawned
// (task 4fe02e30 DONE WHEN clause 2: no binary but `backstory` and the
// terminal/agent launchers). Declared as a qmldir singleton (see qmldir in
// this directory) so both entry points share the exact same instance.
QtObject {
  id: state

  property bool opened: false

  function open() { state.opened = true }
  function close() { state.opened = false }
  function toggle() { state.opened = !state.opened }
}
