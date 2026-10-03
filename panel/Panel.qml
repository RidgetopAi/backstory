import QtQuick
import Quickshell
import Quickshell.Io
import qs.Ui
import qs.Commons
import "js/model.js" as Model
import "js/derive.js" as Derive
import "js/launchers.js" as Launchers
import "js/glyphs.js" as Glyphs
import "js/format.js" as Format

// Backstory: a CLIENT-only panel. It runs `backstory this-week --json --here
// auto` and renders the HERE card, Needs you, and Recent; it never reads the
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
  property string memoryLocation: ""

  readonly property var attentionItems: root.data ? Model.topAttention(root.data) : []
  readonly property var whereLeftOffRows: root.data ? Model.topWhereLeftOff(root.data) : []
  readonly property var weekDays: root.data ? Model.topWeek(root.data) : []
  readonly property var hereData: root.data ? Model.topHere(root.data) : null
  readonly property var hereProject: root.data ? Derive.hereSummary(root.data) : null
  readonly property string hereName: root.hereData ? Model.shortName(Model.hereDisplayName(root.hereData)) : ""
  readonly property string summaryText: Format.summaryLine(Derive.weekTotals(root.weekDays))
  property real nowMs: Date.now()

  // Which group rows are expanded, keyed by group name. Collapsed by
  // default — a group is a human-made summary already; a click reveals it.
  property var expandedGroups: ({})

  // Keyboard navigation runs over the project rows as drawn: standalone
  // rows, and a group's children only while it is expanded.
  readonly property var navRows: {
    var out = []
    for (var i = 0; i < root.whereLeftOffRows.length; i++) {
      var row = root.whereLeftOffRows[i]
      var g = Model.rowGroup(row)
      if (g !== undefined && g !== "") {
        if (root.expandedGroups[g]) out = out.concat(Model.rowChildren(row))
      } else if (Model.rowProject(row)) {
        out.push(Model.rowProject(row))
      }
    }
    return out
  }
  property int selectedIndex: 0
  readonly property var selectedSummary: root.selectedIndex >= 0 && root.selectedIndex < root.navRows.length ? root.navRows[root.selectedIndex] : null
  readonly property string selectedId: root.selectedSummary ? rowId(root.selectedSummary) : ""
  readonly property string footerHint: "j/k move \u00b7 Enter continue \u00b7 t terminal \u00b7 m memory \u00b7 r refresh \u00b7 Esc close"

  function rowId(summary) {
    return Model.summaryProjectKey(summary) + "\n" + Model.summaryCwd(summary)
  }

  // After new data lands, select the `here` project's row, else the first.
  function resetSelection() {
    var idx = 0
    if (root.hereProject) {
      for (var i = 0; i < root.navRows.length; i++) {
        if (rowId(root.navRows[i]) === rowId(root.hereProject)) { idx = i; break }
      }
    }
    root.selectedIndex = idx
  }

  function toggleGroup(group) {
    var next = Object.assign({}, root.expandedGroups)
    next[group] = !next[group]
    root.expandedGroups = next
  }

  function moveSelection(delta) {
    if (root.navRows.length === 0) return
    root.selectedIndex = Math.max(0, Math.min(root.navRows.length - 1, root.selectedIndex + delta))
  }

  function summaryMemory(summary) {
    root.openMemory(Model.summaryProjectKey(summary), Model.summaryDisplayName(summary), Model.summaryCwd(summary) || "")
  }

  // The key handler behind Keys.onPressed on the card; returns whether it
  // consumed the key. Esc closes everywhere; the row keys act only on the
  // main body, never while the group editor or Memory view is showing.
  function handleKey(key) {
    if (key === Qt.Key_Escape) { root.close(); return true }
    if (root.groupEditorOpen || root.memoryOpen) return false
    var s = root.selectedSummary
    switch (key) {
    case Qt.Key_J: case Qt.Key_Down: root.moveSelection(1); return true
    case Qt.Key_K: case Qt.Key_Up: root.moveSelection(-1); return true
    case Qt.Key_Return: case Qt.Key_Enter: root.continueOn(s); return true
    case Qt.Key_T: if (s) root.openTerminal(Model.summaryCwd(s)); return true
    case Qt.Key_M: if (s) root.summaryMemory(s); return true
    case Qt.Key_R: root.refresh(); return true
    }
    return false
  }

  function continueOn(summary) {
    continueAction.continueOn(summary)
  }

  // Test-only handles onto the FloatingWindow's own `id: panel` (harmless on
  // the real desk: FloatingWindow genuinely has contentItem/implicitWidth/
  // implicitHeight — a QML `id` just isn't reachable from outside this
  // file any other way). qmltest/tests/tst_panel.qml's generic layout-
  // bounds walker (task 4fe02e30 DONE WHEN clause 3) uses these.
  readonly property Item testContentItem: panel.contentItem
  readonly property var testWindow: panel
  readonly property Item testKeyItem: card
  readonly property real testImplicitWidth: panel.implicitWidth
  readonly property real testImplicitHeight: panel.implicitHeight

  // Stable window title (Quickshell sets no per-window app_id; Hyprland matches the title) the Hyprland windowrule in
  // ops/hyprland/backstory.conf matches (hyprland_windowrule_test.go and
  // tst_panel_window.qml pin the two together).
  readonly property string windowTitle: "backstory"

  function refresh() {
    root.nowMs = Date.now()
    continueAction.refreshDefaultAgent()
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

  function openMemory(projectKey, displayName, cwd) {
    root.memoryProjectKey = projectKey
    root.memoryDisplayName = displayName
    root.memoryLocation = cwd
    root.memoryOpen = true
  }

  // Scrolls the card's Flickable so `target` lies inside its visible
  // viewport: the user never scrolls to find what a click just produced. An
  // item taller than the viewport is shown from its top.
  function revealInScroller(target) {
    contentColumn.forceLayout()
    var top = target.mapToItem(scroller.contentItem, 0, 0).y
    var bottom = top + target.height
    var y = scroller.contentY
    if (bottom > y + scroller.height) y = bottom - scroller.height
    if (top < y) y = top
    scroller.contentY = Math.max(0, Math.min(y, scroller.contentHeight - scroller.height))
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

  ContinueAction { id: continueAction }

  Process {
    id: dataProcess
    command: Launchers.thisWeekCommand()
    stdout: StdioCollector {
      onStreamFinished: {
        root.loading = false
        try {
          root.data = JSON.parse(text || "{}")
          root.resetSelection()
        } catch (e) {
          root.data = null
        }
      }
    }
  }

  IpcHandler {
    target: "backstory.this-week"
    function open(): string { root.open("{}"); return "ok" }
    function close(): string { root.close(); return "ok" }
    function toggle(): string { root.toggle(); return "ok" }
    function refresh(): string { root.refresh(); return "ok" }
    function ping(): string { return "ok" }
  }

  // A regular xdg toplevel (NOT a wlr-layer-shell PanelWindow): Hyprland
  // manages toplevels, so Omarchy's own move/resize binds (SUPER+drag,
  // resize keys) work on it. ops/hyprland/backstory.conf floats it by
  // windowTitle at a default size/position.
  FloatingWindow {
    id: panel
    title: root.windowTitle
    visible: root.opened
    color: "transparent"
    implicitWidth: card.width
    implicitHeight: card.height

    BorderSurface {
      id: card
      width: Style.space(380)
      height: Math.min(Style.space(640), contentColumn.implicitHeight + Style.spacing.panelPadding * 2)
      color: Util.alpha(Color.popups.background, 0.98)
      borderSpec: Border.surfaceSpec("popups", "border", Color.popups.border, Math.max(1, Style.space(2)))
      radius: Style.cornerRadius

      // Key handling lives on the card so a key reaches it from anywhere in
      // the panel's own subtree; a child that accepts a key (a text field)
      // keeps it.
      focus: root.opened
      Keys.onPressed: (event) => { event.accepted = root.handleKey(event.key) }

      Flickable {
        id: scroller
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
                text: "Backstory"
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

            HereCard {
              visible: root.hereProject !== null
              width: parent.width
              summary: root.hereProject
              hereName: root.hereName
              defaultAgent: continueAction.defaultAgent
              continueLabel: continueAction.label
              week: root.weekDays
              nowMs: root.nowMs
              onContinueRequested: root.continueOn(root.hereProject)
              onTerminalRequested: root.openTerminal(Model.summaryCwd(root.hereProject))
              onMemoryRequested: root.summaryMemory(root.hereProject)
            }

            // Needs you: rendered only when non-empty (PANEL-CONTRACT.md
            // "Attention", decision 9be5c1d5 — an empty array is itself
            // the "nothing to flag" signal, not an absent section).
            Column {
              visible: root.attentionItems.length > 0
              width: parent.width
              spacing: Style.spacing.labelGap

              PanelSectionHeader { text: "NEEDS YOU"; foreground: Color.popups.text }
              AttentionSection { items: root.attentionItems; width: parent.width }
            }

            Column {
              width: parent.width
              spacing: Style.spacing.labelGap

              PanelSectionHeader { text: "RECENT"; foreground: Color.popups.text }
              WhereLeftOffSection {
                rows: root.whereLeftOffRows
                week: root.weekDays
                expandedGroups: root.expandedGroups
                selectedId: root.selectedId
                width: parent.width
                onToggleGroup: (group) => root.toggleGroup(group)
                onOpenTerminal: (cwd) => root.openTerminal(cwd)
                onContinueRequested: (summary) => root.continueOn(summary)
                onOpenMemory: (projectKey, displayName, cwd) => root.openMemory(projectKey, displayName, cwd)
              }
            }

            Text {
              width: parent.width
              textFormat: Text.PlainText
              text: root.summaryText
              font.family: Style.font.family
              font.pixelSize: Style.font.caption
              color: Qt.darker(Color.popups.text, 1.3)
              wrapMode: Text.WordWrap
            }

            Text {
              width: parent.width
              textFormat: Text.PlainText
              text: root.footerHint
              font.family: Style.font.family
              font.pixelSize: Style.font.caption
              color: Qt.darker(Color.popups.text, 1.3)
              wrapMode: Text.WordWrap
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
            location: root.memoryLocation
            onReveal: (target) => root.revealInScroller(target)
            onCloseRequested: root.memoryOpen = false
            onChanged: root.refresh()
          }
        }
      }
    }
  }
}
