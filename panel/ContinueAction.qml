import QtQuick
import Quickshell.Io
import "js/model.js" as Model
import "js/launchers.js" as Launchers
import "js/format.js" as Format

// Continue: the one action behind the panel's primary button, a row's
// resume button, Enter, and the bar widget's right click. It focuses the
// project's already-open window when `window` names one (a validated
// Hyprland address) and, when `tmux` carries a valid target, selects that
// tmux window and pane; else it launches Omarchy's default agent on the
// handoff in the project's cwd; with no default agent it opens the picker instead.
// It also owns the read of that default (`omarchy-default-agent`).
Item {
  id: root

  property string defaultAgent: ""
  readonly property string label: Format.continueLabel(root.defaultAgent)

  // Falls back to copying the handoff id when the agent launch fails, so
  // the user can still paste it (AGENT-CONTRACT.md "Cross-agent handoff").
  property string pendingHandoffId: ""
  property string pendingWindow: ""

  // tmux select argvs still to run, one at a time, in order.
  property var tmuxQueue: []

  function runNextTmux() {
    if (root.tmuxQueue.length === 0) return
    tmuxProcess.command = root.tmuxQueue[0] // Launchers.tmuxSelect*Command argv
    root.tmuxQueue = root.tmuxQueue.slice(1)
    tmuxProcess.running = true
  }

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
    var plan = Launchers.continueFocusPlan(Model.summaryWindow(summary) || "", Model.summaryTmux(summary) || "")
    if (plan) {
      root.pendingWindow = Model.summaryWindow(summary)
      focusProcess.command = Launchers.focusWindowCommand(root.pendingWindow)
      focusProcess.running = true
      root.tmuxQueue = plan.tmuxCommands
      root.runNextTmux()
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
    id: tmuxProcess
    onExited: (exitCode, exitStatus) => {
      if (exitCode !== 0) root.tmuxQueue = []
      else root.runNextTmux()
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
