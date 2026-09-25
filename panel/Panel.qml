import QtQuick
import Quickshell
import Quickshell.Io
import Quickshell.Wayland
import qs.Ui
import qs.Commons
import "js/model.js" as Model
import "js/launchers.js" as Launchers
import "js/glyphs.js" as Glyphs

// This Week: a CLIENT-only panel. It runs `backstory this-week --json` and
// renders Attention, Where you left off, and The week; it never reads the
// store itself (PLAN.md Phase 4 "Human look", decision 9be5c1d5). The host
// injects `open(payloadJson)` / `close()` per the "panel" plugin kind
// contract (docs/omarchy-shell.md "Plugin manifest").
Item {
  id: root

  // opened mirrors PanelState.opened (a qmldir singleton, see PanelState.qml)
  // rather than owning its own stored flag, so BarWidget.qml's click
  // (manifest.json "bar-widget" kind, round 2 defect B) and the host's own
  // open()/close()/toggle() calls (the "panel" kind contract) both change
  // the one true value. Nothing here may assign root.opened directly — that
  // would sever the binding — every state change goes through PanelState's
  // own open()/close()/toggle().
  readonly property bool opened: PanelState.opened
  property var data: null
  property bool loading: false
  property bool groupEditorOpen: false

  readonly property var attentionItems: root.data ? Model.topAttention(root.data) : []
  readonly property var whereLeftOffRows: root.data ? Model.topWhereLeftOff(root.data) : []
  readonly property var weekDays: root.data ? Model.topWeek(root.data) : []

  function refresh() {
    root.loading = true
    dataProcess.running = false
    dataProcess.running = true
  }

  // The ONE place this plugin reacts to the panel becoming visible,
  // regardless of which entry point flipped PanelState.opened — the bar
  // widget's click (BarWidget.qml calls PanelState.toggle() directly, not
  // a function here) and the host's own IPC open()/toggle() both land on
  // this same PanelState.opened change (round 3 defect A: "nothing reacts
  // to PanelState.opened becoming true", so a bar click opened an empty
  // panel). open()/toggle() below intentionally do NOT also call
  // refresh() themselves — one handler, not a second copy per entry
  // point.
  onOpenedChanged: {
    if (root.opened) root.refresh()
  }

  function open(payloadJson) {
    PanelState.open()
  }

  function close() {
    PanelState.close()
    root.groupEditorOpen = false
  }

  function toggle() {
    root.opened ? root.close() : root.open("{}")
  }

  // Click anywhere on a row opens a terminal at that project's cwd
  // (AGENT-CONTRACT.md: "same-agent resume is reopening a terminal at the
  // session's cwd"). --dir carries the target explicitly; workingDirectory
  // is set too so a terminal launcher that ignores --dir still lands right.
  function openTerminal(cwd) {
    Quickshell.execDetached({
      command: Launchers.terminalLaunchCommand(cwd),
      workingDirectory: cwd
    })
  }

  // "Resume in <agent>": `omarchy agent prompt <handoff text>`, started in
  // the project's own cwd. Falls back to copying the handoff id to the
  // clipboard when the launch itself fails to start an agent
  // (AGENT-CONTRACT.md "Cross-agent handoff", original description's own
  // "fallback: copy the handoff id").
  property string pendingHandoffId: ""

  function resumeAgent(cwd, handoffText, handoffId) {
    root.pendingHandoffId = handoffId
    agentProcess.workingDirectory = cwd
    agentProcess.command = Launchers.agentPromptCommand(handoffText)
    agentProcess.running = true
  }

  function copyToClipboard(text) {
    clipboardHelper.text = text
    clipboardHelper.selectAll()
    clipboardHelper.copy()
  }

  Process {
    id: dataProcess
    command: Launchers.thisWeekCommand()
    stdout: StdioCollector {
      onStreamFinished: {
        root.loading = false
        try {
          root.data = JSON.parse(text || "{}")
        } catch (e) {
          root.data = null
        }
      }
    }
  }

  Process {
    id: agentProcess
    onExited: (exitCode, exitStatus) => {
      if (exitCode !== 0) root.copyToClipboard(root.pendingHandoffId)
    }
  }

  TextEdit {
    id: clipboardHelper
    visible: false
  }

  IpcHandler {
    target: "backstory.this-week"
    function open(): string { root.open("{}"); return "ok" }
    function close(): string { root.close(); return "ok" }
    function toggle(): string { root.toggle(); return "ok" }
    function refresh(): string { root.refresh(); return "ok" }
    function ping(): string { return "ok" }
  }

  PanelWindow {
    id: panel
    visible: root.opened
    anchors { top: true; right: true }
    margins { top: Style.space(8); right: Style.space(8) }
    color: "transparent"
    WlrLayershell.namespace: "backstory-this-week"
    WlrLayershell.layer: WlrLayer.Top
    WlrLayershell.keyboardFocus: WlrKeyboardFocus.OnDemand
    exclusionMode: ExclusionMode.Ignore
    implicitWidth: card.width
    implicitHeight: card.height

    BorderSurface {
      id: card
      width: Style.space(380)
      height: Math.min(Style.space(640), body.implicitHeight + Style.spacing.panelPadding * 2)
      color: Util.alpha(Color.popups.background, 0.98)
      borderSpec: Border.surfaceSpec("popups", "border", Color.popups.border, Math.max(1, Style.space(2)))
      radius: Style.cornerRadius

      Flickable {
        anchors.fill: parent
        anchors.margins: Style.spacing.panelPadding
        contentWidth: width
        contentHeight: body.implicitHeight
        clip: true

        Column {
          id: body
          width: parent.width
          spacing: Style.spacing.panelGap

          Row {
            width: parent.width

            Text {
              textFormat: Text.PlainText
              text: "This Week"
              font.family: Style.font.family
              font.pixelSize: Style.font.heading
              font.bold: true
              color: Color.popups.text
              width: parent.width - Style.space(56)
            }

            PanelActionButton {
              iconText: Glyphs.gear()
              tooltipText: "Groups"
              foreground: Color.popups.text
              hoverColor: Color.accent
              onClicked: root.groupEditorOpen = !root.groupEditorOpen
            }

            PanelActionButton {
              iconText: Glyphs.close()
              tooltipText: "Close"
              foreground: Color.popups.text
              hoverColor: Color.urgent
              onClicked: root.close()
            }
          }

          // Attention: rendered only when non-empty (PANEL-CONTRACT.md
          // "Attention", decision 9be5c1d5 — an empty array is itself the
          // "nothing to flag" signal, not an absent section).
          Column {
            visible: root.attentionItems.length > 0
            width: parent.width
            spacing: Style.spacing.labelGap

            PanelSectionHeader { text: "ATTENTION"; foreground: Color.popups.text }
            AttentionSection { items: root.attentionItems; width: parent.width }
          }

          Column {
            width: parent.width
            spacing: Style.spacing.labelGap

            PanelSectionHeader { text: "WHERE YOU LEFT OFF"; foreground: Color.popups.text }
            WhereLeftOffSection {
              rows: root.whereLeftOffRows
              width: parent.width
              onOpenTerminal: (cwd) => root.openTerminal(cwd)
              onResumeAgent: (cwd, handoffText, handoffId) => root.resumeAgent(cwd, handoffText, handoffId)
            }
          }

          Column {
            width: parent.width
            spacing: Style.spacing.labelGap

            PanelSectionHeader { text: "THE WEEK"; foreground: Color.popups.text }
            WeekSection { days: root.weekDays; width: parent.width }
          }
        }
      }
    }

    GroupEditor {
      visible: root.groupEditorOpen
      anchors.top: card.top
      anchors.right: card.left
      anchors.rightMargin: Style.space(8)
      onCloseRequested: root.groupEditorOpen = false
      onChanged: root.refresh()
    }
  }
}
