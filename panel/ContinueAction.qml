import QtQuick
import Quickshell.Io
import "js/model.js" as Model
import "js/launchers.js" as Launchers
import "js/format.js" as Format

// Continue: the one action behind the panel's primary button, a row's
// resume button, Enter, and the bar widget's right click. It focuses the
// project's already-open window when `window` names one (a validated
// Hyprland address) and, when `tmux` carries a valid target, selects that
// tmux window and pane; else it launches the row's newest agent (or the
// sub-row's chosen one) from Launchers.LAUNCH_TABLE on the handoff in the
// project's cwd; an agent outside the table falls back to Omarchy's default
// agent, and with no default agent it opens the picker instead.
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

  // The agent Continue launches on a folder row: agents[0] (newest) when
  // the launch table knows it, else "" (the omarchy default-agent path).
  function launchAgent(summary) {
    if (!summary) return ""
    var agents = Model.summaryAgents(summary)
    var id = agents.length > 0 ? (Model.agentId(agents[0]) || "") : ""
    return Launchers.hasLauncher(id) ? id : ""
  }

  // Label of a row's primary Continue button: names the agent it launches.
  function labelFor(summary) {
    var id = root.launchAgent(summary)
    return id !== "" ? "Continue in " + Format.agentName(id) : root.label
  }

  // `agent` is a sub-row's own choice: it always launches that agent (never
  // a window focus). Without it (a folder row) an already-open window is
  // focused as before, else agents[0] launches, else the omarchy default
  // agent does (or the picker when none is set).
  function continueOn(summary, agent) {
    if (!summary) return
    var chosen = agent || ""
    if (chosen === "") {
      var plan = Launchers.continueFocusPlan(Model.summaryWindow(summary) || "", Model.summaryTmux(summary) || "")
      if (plan) {
        root.pendingWindow = Model.summaryWindow(summary)
        focusProcess.command = Launchers.focusWindowCommand(root.pendingWindow)
        focusProcess.running = true
        root.tmuxQueue = plan.tmuxCommands
        root.runNextTmux()
        return
      }
      chosen = root.launchAgent(summary)
    }
    var cwd = Model.summaryCwd(summary) || ""
    var id = Model.summaryHandoffId(summary) || ""
    var prompt = Format.handoffPrompt(Model.summaryDisplayName(summary), id, Model.summaryHandoffNext(summary) || "")
    var launch = chosen !== "" ? Launchers.agentLaunchCommand(chosen, cwd, prompt) : null
    if (!launch) {
      if (root.defaultAgent === "") {
        pickProcess.command = Launchers.agentPickCommand()
        pickProcess.running = true
        return
      }
      launch = Launchers.agentPromptCommand(prompt)
    }
    root.pendingHandoffId = id
    agentProcess.workingDirectory = cwd
    agentProcess.command = launch
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
