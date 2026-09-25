pragma Singleton
import QtQuick

// Minimal stand-in for the real Quickshell module's global Quickshell
// singleton. The panel's own launchers.js is the only place a command is
// named (js/launchers.js's own doc comment) — this stub RECORDS every
// execDetached() call instead of actually spawning anything, so a QML test
// can assert on the exact argv a click produced (task 4fe02e30 DONE WHEN
// clause 4).
QtObject {
  id: root

  // Each entry: { command: [...], workingDirectory: "" }
  property var execDetachedCalls: []

  function execDetached(arg) {
    var command = []
    var workingDirectory = ""
    if (Array.isArray(arg)) {
      command = arg
    } else if (arg && arg.command) {
      command = arg.command
      workingDirectory = arg.workingDirectory || ""
    }
    var calls = root.execDetachedCalls.slice()
    calls.push({ command: command, workingDirectory: workingDirectory })
    root.execDetachedCalls = calls
  }

  function reset() {
    root.execDetachedCalls = []
  }

  function lastCall() {
    return root.execDetachedCalls.length > 0
      ? root.execDetachedCalls[root.execDetachedCalls.length - 1]
      : null
  }
}
