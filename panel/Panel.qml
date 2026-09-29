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

  // Owned locally (round 4: no more PanelState singleton — see
  // BarWidget.qml's own doc comment. BarWidget's click no longer touches
  // this plugin's own QML state at all; it fires `omarchy-shell shell
  // toggle backstory.this-week {}` and trusts the host to call back into
  // the "panel" kind contract's own open()/close()/toggle() below, the
  // same functions IpcHandler answers).
  property bool opened: false
  property var data: null
  property bool loading: false
  property bool groupEditorOpen: false
  property bool memoryOpen: false
  property string memoryProjectKey: ""
  property string memoryDisplayName: ""

  readonly property var attentionItems: root.data ? Model.topAttention(root.data) : []
  readonly property var whereLeftOffRows: root.data ? Model.topWhereLeftOff(root.data) : []
  readonly property var weekDays: root.data ? Model.topWeek(root.data) : []

  // Test-only handles onto the PanelWindow's own `id: panel` (harmless on
  // the real desk: PanelWindow genuinely has contentItem/implicitWidth/
  // implicitHeight — a QML `id` just isn't reachable from outside this
  // file any other way). qmltest/tests/tst_panel.qml's generic layout-
  // bounds walker (task 4fe02e30 DONE WHEN clause 3) uses these.
  readonly property Item testContentItem: panel.contentItem
  readonly property real testImplicitWidth: panel.implicitWidth
  readonly property real testImplicitHeight: panel.implicitHeight

  function refresh() {
    root.loading = true
    dataProcess.running = false
    dataProcess.running = true
  }

  // The ONE place this plugin reacts to the panel becoming visible,
  // regardless of which entry point flipped root.opened — open()/toggle()
  // below intentionally do NOT also call refresh() themselves — one
  // handler, not a second copy per entry point.
  onOpenedChanged: {
    if (root.opened) root.refresh()
  }

  function open(payloadJson) {
    root.opened = true
  }

  function close() {
    root.opened = false
    root.groupEditorOpen = false
    root.memoryOpen = false
  }

  function openMemory(projectKey, displayName) {
    root.memoryProjectKey = projectKey
    root.memoryDisplayName = displayName
    root.memoryOpen = true
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
    exclusionMode: ExclusionMode.Ignore
    implicitWidth: card.width
    implicitHeight: card.height

    // Not every compositor backs PanelWindow with wlr-layer-shell (X11,
    // some non-wlroots Wayland compositors) — WlrLayershell is then simply
    // absent. This is real Quickshell/WlrLayershell's own documented
    // portability pattern (`if (this.WlrLayershell != null) { ... }`), not
    // a workaround invented for this plugin's own test harness — see
    // qmltest/stubs/Quickshell/PanelWindow.qml's doc comment for why a
    // static `WlrLayershell.namespace: value` binding (round 3's form)
    // cannot be loaded by a plain Qt/QtTest engine at all, attached or not.
    Component.onCompleted: {
      if (panel.WlrLayershell) {
        panel.WlrLayershell.namespace = "backstory-this-week"
        panel.WlrLayershell.layer = WlrLayer.Top
        panel.WlrLayershell.keyboardFocus = WlrKeyboardFocus.OnDemand
      }
    }

    BorderSurface {
      id: card
      width: Style.space(380)
      height: Math.min(Style.space(640), contentColumn.implicitHeight + Style.spacing.panelPadding * 2)
      color: Util.alpha(Color.popups.background, 0.98)
      borderSpec: Border.surfaceSpec("popups", "border", Color.popups.border, Math.max(1, Style.space(2)))
      radius: Style.cornerRadius

      Flickable {
        anchors.fill: parent
        anchors.margins: Style.spacing.panelPadding
        contentWidth: width
        contentHeight: contentColumn.implicitHeight
        clip: true

        // Round 4 desk defect B: GroupEditor used to be a sibling of
        // `card` anchored `anchors.right: card.left` — outside the
        // PanelWindow's own surface (implicitWidth/Height are card's own
        // width/height, so anything anchored outside card never had a
        // window to render into). Now it lives INSIDE this Flickable, as
        // one more child of `card`, so it is always within the window's
        // implicit bounds; the gear/back control swaps which one is
        // visible instead of opening a second surface.
        Column {
          id: contentColumn
          width: parent.width

          Column {
            id: body
            visible: !root.groupEditorOpen && !root.memoryOpen
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
            // "Attention", decision 9be5c1d5 — an empty array is itself
            // the "nothing to flag" signal, not an absent section).
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
                onOpenMemory: (projectKey, displayName) => root.openMemory(projectKey, displayName)
              }
            }

            Column {
              width: parent.width
              spacing: Style.spacing.labelGap

              PanelSectionHeader { text: "THE WEEK"; foreground: Color.popups.text }
              WeekSection { days: root.weekDays; width: parent.width }
            }
          }

          GroupEditor {
            visible: root.groupEditorOpen
            width: parent.width
            onCloseRequested: root.groupEditorOpen = false
            onChanged: root.refresh()
          }

          MemoryView {
            visible: root.memoryOpen
            width: parent.width
            projectKey: root.memoryProjectKey
            displayName: root.memoryDisplayName
            onCloseRequested: root.memoryOpen = false
            onChanged: root.refresh()
          }
        }
      }
    }
  }
}
