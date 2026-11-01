pragma Singleton
import QtQuick

// Minimal stand-in for Quickshell.Hyprland's Hyprland singleton: the
// windows and focused workspace a test sets, and a dispatch() that RECORDS
// each request instead of sending it, so a test can assert on it.
QtObject {
  id: root

  // Each toplevel: { address, title, activated, workspace: { id }, wayland: { appId } }
  property var toplevels: ({ values: [] })
  property var focusedWorkspace: null
  property var focusedMonitor: null
  property var dispatched: []

  function dispatch(request) {
    root.dispatched = root.dispatched.concat([request])
  }

  function reset() {
    root.toplevels = { values: [] }
    root.focusedWorkspace = null
    root.focusedMonitor = null
    root.dispatched = []
  }
}
