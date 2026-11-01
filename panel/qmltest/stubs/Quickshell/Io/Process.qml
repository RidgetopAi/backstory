import QtQuick

// Minimal stand-in for Quickshell.Io's Process — "Process records its
// command; the test feeds stdout + exit code" (task 4fe02e30's harness
// spec). Setting `running` to true synchronously records the argv (and
// working directory) into ProcessController, looks up the canned response
// a test registered for that exact argv (ProcessController.respond,
// defaulting to empty stdout/stderr and exit 0 for anything unregistered),
// feeds it to `stdout`/`stderr`, and fires `exited` — all synchronously, so
// a QML test never needs to spin an event loop waiting for a real
// subprocess (see ProcessController.qml's own doc comment). A held
// response stays `running` until the test releases it.
//
// Setting `running` to false on a held run ends it as Quickshell's
// process.cpp does: terminate() (SIGTERM), then on the run's finish the
// collectors' streamFinished with the output so far (none: a held run has
// written nothing), then exited with a crash (15, QProcess.CrashExit). A
// `running = true` that follows starts the next run only after that, as
// Quickshell's onFinished does for `running = false; running = true`.
QtObject {
  id: proc

  property var command: []
  property string workingDirectory: ""
  property bool running: false
  property var stdout: null
  property var stderr: null

  signal exited(int exitCode, int exitStatus)

  // The held run this Process is waiting on, or null.
  property var heldRun: null

  onRunningChanged: {
    if (!running) {
      if (proc.heldRun) proc.terminate()
      return
    }

    ProcessController.recordRun(proc.command, proc.workingDirectory)
    var response = ProcessController.lookup(proc.command)
    if (response.held) {
      proc.heldRun = ProcessController.hold(proc.command, function () { proc.heldRun = null; proc.deliver(response) })
      return
    }
    proc.deliver(response)
  }

  function terminate() {
    ProcessController.drop(proc.heldRun)
    proc.heldRun = null
    proc.deliver({ stdout: "", stderr: "", exitCode: 15, exitStatus: 1 })
  }

  function deliver(response) {
    if (proc.stdout) proc.stdout.text = response.stdout || ""
    if (proc.stderr) proc.stderr.text = response.stderr || ""

    proc.running = false

    if (proc.stdout) proc.stdout.streamFinished()
    if (proc.stderr) proc.stderr.streamFinished()
    proc.exited(response.exitCode || 0, response.exitStatus || 0)
  }
}
