import QtQuick

// Minimal stand-in for Quickshell.Io's Process — "Process records its
// command; the test feeds stdout + exit code" (task 4fe02e30's harness
// spec). Setting `running` to true synchronously records the argv (and
// working directory) into ProcessController, looks up the canned response
// a test registered for that exact argv (ProcessController.respond,
// defaulting to empty stdout/stderr and exit 0 for anything unregistered),
// feeds it to `stdout`/`stderr`, and fires `exited` — all synchronously, so
// a QML test never needs to spin an event loop waiting for a real
// subprocess (see ProcessController.qml's own doc comment).
QtObject {
  id: proc

  property var command: []
  property string workingDirectory: ""
  property bool running: false
  property var stdout: null
  property var stderr: null

  signal exited(int exitCode, int exitStatus)

  onRunningChanged: {
    if (!running) return

    ProcessController.recordRun(proc.command, proc.workingDirectory)
    var response = ProcessController.lookup(proc.command)

    if (proc.stdout) proc.stdout.text = response.stdout || ""
    if (proc.stderr) proc.stderr.text = response.stderr || ""

    proc.running = false

    if (proc.stdout) proc.stdout.streamFinished()
    if (proc.stderr) proc.stderr.streamFinished()
    proc.exited(response.exitCode || 0, 0)
  }
}
