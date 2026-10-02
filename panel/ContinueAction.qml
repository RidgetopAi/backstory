import QtQuick
import Quickshell.Io
import "js/model.js" as Model
import "js/launchers.js" as Launchers
import "js/format.js" as Format

// Continue: the one action behind the panel's primary button, a row's
// resume button, Enter, and the bar widget's right click. It focuses the
// project's already-open window when `window` names one (a validated
// Hyprland address), else launches Omarchy's default agent on the handoff
// in the project's cwd; with no default agent it opens the picker instead.
// It also owns the read of that default (`omarchy-default-agent`).
Item {
  id: root

  property string defaultAgent: ""
  readonly property string label: Format.continueLabel(root.defaultAgent)

  // Falls back to copying the handoff id when the agent launch fails, so
  // the user can still paste it (AGENT-CONTRACT.md "Cross-agent handoff").
  property string pendingHandoffId: ""
  property string pendingWindow: ""

  function refreshDefaultAgent() {
    defaultAgentProcess.running = false
    defaultAgentProcess.running = true
  }

  function continueOn(summary) {
    if (root.defaultAgent === "") {
      pickProcess.command = Launchers.agentPickCommand()
      pickProcess.running = true
      return
    }
    if (!summary) return
    var addr = Model.summaryWindow(summary) || ""
    if (Launchers.isWindowAddress(addr)) {
      root.pendingWindow = addr
      focusProcess.command = Launchers.focusWindowCommand(addr)
      focusProcess.running = true
      return
    }
    var id = Model.summaryHandoffId(summary) || ""
    root.pendingHandoffId = id
    agentProcess.workingDirectory = Model.summaryCwd(summary) || ""
    agentProcess.command = Launchers.agentPromptCommand(
      Format.handoffPrompt(Model.summaryDisplayName(summary), id, Model.summaryHandoffNext(summary) || ""))
    agentProcess.running = true
  }

  function copyToClipboard(text) {
    clipboardHelper.text = text
    clipboardHelper.selectAll()
    clipboardHelper.copy()
  }

  Process {
    id: defaultAgentProcess
    command: Launchers.defaultAgentCommand()
    stdout: StdioCollector {
      onStreamFinished: root.defaultAgent = (text || "").trim()
    }
  }

  Process { id: pickProcess }

  Process {
    id: focusProcess
    onExited: (exitCode, exitStatus) => {
      if (exitCode !== 0 && root.pendingWindow !== "") {
        var addr = root.pendingWindow
        root.pendingWindow = ""
        focusProcess.command = Launchers.focusWindowFallbackCommand(addr)
        focusProcess.running = true
      }
    }
  }

  Process {
    id: agentProcess
    onExited: (exitCode, exitStatus) => {
      if (exitCode !== 0 && root.pendingHandoffId !== "") root.copyToClipboard(root.pendingHandoffId)
    }
  }

  TextEdit {
    id: clipboardHelper
    visible: false
  }
}
