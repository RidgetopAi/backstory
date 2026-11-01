import QtQuick
import Quickshell
import Quickshell.Io
import qs.Ui
import qs.Commons
import qs.Commons as Commons
import "js/model.js" as Model
import "js/derive.js" as Derive
import "js/launchers.js" as Launchers
import "js/glyphs.js" as Glyphs
import "js/scroll.js" as Scroll
import "js/format.js" as Format

// Backstory's this-week content: the HERE card, Needs you and Recent, with
// their data, selection and keys. It runs `backstory this-week --json --here
// auto` and never reads the store itself (PLAN.md Phase 4 "Human look",
// decision 9be5c1d5). A host surface (Panel.qml's window) binds `active` to
// whether it is showing and closes itself on closeRequested.
Item {
  id: root

  // True while the host surface is showing; turning on refreshes the data,
  // turning off returns to the main body.
  property bool active: false
  signal closeRequested()
  // A host that can show the view somewhere roomier (the bar popup, which
  // opens the window) sets canPopOut; the window sets canPin and pinned
  // (pinned, it follows you to every workspace). Every view's header then
  // offers those buttons (HostButtons).
  // Off while restore() sets the view (SlidePage animate).
  property bool slideAnimated: true
  property bool canPopOut: false
  property bool canPin: false
  property bool pinned: false
  signal popOutRequested()
  signal pinRequested()

  property var weekData: null
  property bool loading: false
  // A fresh panel (the shell unloads it on a bar or keybind close) has no
  // data until this-week first answers; until then it shows no empty state.
  property bool hasLoaded: false
  readonly property bool waitingForFirstData: root.loading && !root.hasLoaded
  property bool groupEditorOpen: false
  property bool memoryOpen: false
  property bool helpOpen: false
  // Whichever way a view comes in, it lands under a still pointer. One
  // view opens at a time, the latest: the main view stays live while
  // Memory waits for its records, and its gear then opens Groups instead.
  onGroupEditorOpenChanged: {
    pointerGate.reset()
    if (root.groupEditorOpen) { root.memoryOpen = false; root.helpOpen = false }
    // Left from the group-name field: the keys come back here (and on to
    // the host's handler), not to a hidden field.
    else if (groupEditor.typing) root.forceActiveFocus()
  }
  onMemoryOpenChanged: {
    pointerGate.reset()
    root.memoryWaitOver = false
    if (!root.slideAnimated) Qt.callLater(root.slideAgain)
    if (root.memoryOpen) {
      root.groupEditorOpen = false
      root.helpOpen = false
      memoryWait.restart()
    }
  }
  // Memory slides in once its records are here, or after memoryWait with
  // "Loading records..." if they are slow; the main view stays till then.
  property bool memoryWaitOver: false
  readonly property bool memoryShowing: root.memoryOpen && (memoryPane.ready || root.memoryWaitOver)
  onHelpOpenChanged: {
    pointerGate.reset()
    if (root.helpOpen) { root.groupEditorOpen = false; root.memoryOpen = false }
  }
  property string memoryProjectKey: ""
  property string memoryDisplayName: ""
  property string memoryLocation: ""

  readonly property var attentionItems: root.weekData ? Model.topAttention(root.weekData) : []
  readonly property var whereLeftOffRows: root.weekData ? Model.topWhereLeftOff(root.weekData) : []
  readonly property var weekDays: root.weekData ? Model.topWeek(root.weekData) : []
  readonly property var hereData: root.weekData ? Model.topHere(root.weekData) : null
  readonly property var hereProject: root.weekData ? Derive.hereSummary(root.weekData) : null
  readonly property string hereName: root.hereData ? Model.shortName(Model.hereDisplayName(root.hereData)) : ""
  readonly property string summaryText: Format.summaryLine(Derive.weekTotals(root.weekDays))
  property real nowMs: Date.now()

  // The content's natural height, padding included; the host sizes to it.
  implicitHeight: contentColumn.implicitHeight + root.edge
    + root.footerGap + footer.implicitHeight + root.footerBottomMargin
  // The key hints sit in a fixed footer under the scrolling content, in every view.
  readonly property real footerGap: Style.spacing.labelGap * 2
  // The space between the content and the host's edge, and below the key
  // hints. The window draws its own card; a host that pads its card itself
  // (the bar popup's KeyboardPanel) sets both to 0.
  property real edge: Style.spacing.panelPadding
  property real footerBottomMargin: Style.space(12)
  // The scrolling part of the view showing (each view's header stays put).
  readonly property Item scrollArea: root.memoryOpen ? memoryPane.flickable
    : root.groupEditorOpen ? groupEditor.flickable
    : root.helpOpen ? helpPane.flickable : mainScroll.flickable
  readonly property Item memoryView: memoryPane

  // Which group rows are expanded, keyed by group name. Collapsed by
  // default — a group is a human-made summary already; a click reveals it.
  property var expandedGroups: ({})

  // Which folder rows show their per-agent sub-rows, keyed by rowId (row
  // key plus cwd). Collapsed by default.
  property var expandedRows: ({})

  // Keyboard navigation runs over the rows as drawn: standalone rows, a
  // group's children only while it is expanded, and an expanded folder
  // row's per-agent sub-rows right after it. Each entry is
  // {summary, agent}; agent is "" for a folder row.
  readonly property var navRows: {
    var out = []
    function add(summary) {
      out.push({ summary: summary, agent: "" })
      if (root.expandedRows[root.rowId(summary)]) {
        var agents = Model.summaryAgents(summary)
        for (var j = 0; j < agents.length; j++) out.push({ summary: summary, agent: Model.agentId(agents[j]) })
      }
    }
    for (var i = 0; i < root.whereLeftOffRows.length; i++) {
      var row = root.whereLeftOffRows[i]
      var g = Model.rowGroup(row)
      if (g !== undefined && g !== "") {
        if (root.expandedGroups[g]) {
          var kids = Model.rowChildren(row)
          for (var k = 0; k < kids.length; k++) add(kids[k])
        }
      } else if (Model.rowProject(row)) {
        add(Model.rowProject(row))
      }
    }
    return out
  }
  property int selectedIndex: 0
  // The selected row itself, {id, agent}: rows expanding above it or a
  // reload move its index, not the selection. Null until a load selects.
  property var selectedKey: null
  onSelectedIndexChanged: root.rememberSelection()
  function rememberSelection() {
    // Read here, not through selectedEntry, which may not have caught up.
    var e = root.navRows[root.selectedIndex]
    root.selectedKey = e ? { id: root.rowId(e.summary), agent: e.agent } : null
  }
  property var lastNavRows: []
  onNavRowsChanged: {
    var old = root.lastNavRows
    root.lastNavRows = root.navRows
    if (root.selectedKey === null) return
    root.selectedIndex = root.indexForKey(root.selectedKey, old)
    root.rememberSelection()
  }
  readonly property var selectedEntry: root.selectedIndex >= 0 && root.selectedIndex < root.navRows.length ? root.navRows[root.selectedIndex] : null
  readonly property var selectedSummary: root.selectedEntry ? root.selectedEntry.summary : null
  readonly property string selectedAgent: root.selectedEntry ? root.selectedEntry.agent : ""
  readonly property string selectedId: root.selectedSummary ? rowId(root.selectedSummary) : ""
  // Backstory's glyph (as the bar widget): md-script_text, an ancient scroll.
  readonly property string scrollGlyph: String.fromCodePoint(0xF0BC2)
  readonly property var weekTotals: Derive.weekTotals(root.weekDays)
  // The hero's status line: what needs you, otherwise this week's count.
  readonly property string heroStatus: root.attentionItems.length > 0
    ? root.attentionItems.length + (root.attentionItems.length === 1 ? " needs you" : " need you")
    : root.weekTotals.projects + " projects \u00b7 " + root.weekTotals.sessions + " sessions this week"
  // Every view's footer ends with the keys the host adds: o where it can
  // pop out (the bar popup), p where it can pin (the window), and Esc.
  readonly property string closeHint: (root.canPopOut ? "o pop out \u00b7 " : "")
    + (root.canPin ? (root.pinned ? "p unpin \u00b7 " : "p pin \u00b7 ") : "") + "Esc close"
  readonly property string mainFooterHint: "j/k move \u00b7 \u2192 agents \u00b7 \u2190 fold \u00b7 Enter continue \u00b7 t terminal \u00b7 m memory \u00b7 g groups \u00b7 ? help \u00b7 r refresh \u00b7 " + root.closeHint
  readonly property string footerHint: root.helpOpen ? "j/k scroll \u00b7 u or ? back \u00b7 " + root.closeHint
    : root.memoryOpen ? "j/k move \u00b7 \u2192 expand \u00b7 \u2190 fold \u00b7 u back \u00b7 " + root.closeHint
    : root.groupEditorOpen ? "j/k move \u00b7 Enter pick \u00b7 u back \u00b7 " + root.closeHint : root.mainFooterHint

  // Hover moves the one selection the keyboard uses (no second highlight).
  function selectRow(summary, agent) {
    for (var i = 0; i < root.navRows.length; i++) {
      if (root.navRows[i].agent === agent && rowId(root.navRows[i].summary) === rowId(summary)) { root.selectedIndex = i; return }
    }
  }

  function rowId(summary) {
    return Model.summaryProjectKey(summary) + "\n" + Model.summaryCwd(summary)
  }

  function indexOfRow(id, agent, rows) {
    for (var i = 0; i < rows.length; i++) if (rows[i].agent === agent && root.rowId(rows[i].summary) === id) return i
    return -1
  }

  // Where the selected row is now; when it is gone, its folder row (for an
  // agent sub-row), else the nearest row that was around it and still is
  // (the one after first), else the here row.
  function indexForKey(key, oldRows) {
    var i = root.indexOfRow(key.id, key.agent, root.navRows)
    if (i < 0 && key.agent !== "") i = root.indexOfRow(key.id, "", root.navRows)
    if (i >= 0) return i
    var at = root.indexOfRow(key.id, key.agent, oldRows)
    for (var d = 1; at >= 0 && (at + d < oldRows.length || at - d >= 0); d++) {
      var near = [oldRows[at + d], oldRows[at - d]]
      for (var n = 0; n < 2; n++) {
        if (!near[n]) continue
        i = root.indexOfRow(root.rowId(near[n].summary), near[n].agent, root.navRows)
        if (i >= 0) return i
      }
    }
    return root.hereIndex()
  }

  // The `here` project's row, else the first.
  function hereIndex() {
    var i = root.hereProject ? root.indexOfRow(root.rowId(root.hereProject), "", root.navRows) : -1
    return i < 0 ? 0 : i
  }

  // Selects the `here` project's row, else the first: the first data of
  // an opening, or nothing to keep.
  function resetSelection() {
    root.selectedIndex = root.hereIndex()
    root.rememberSelection()
  }

  // ---- Snapshot and restore: the bar popup's pop-out opens the window
  // where you were. A snapshot names the view showing, Memory's project and
  // its own state, the selection, the expanded rows and groups, and the
  // scroll. restore() takes it from the summon payload, so it trusts nothing:
  // wrong types and long strings are dropped, and a missing view is the main
  // one. The selection waits for the next data load unless data is here.
  property var pendingSelection: null
  property real pendingScrollY: 0

  function snapshot() {
    var view = root.groupEditorOpen ? "groups" : root.memoryOpen ? "memory" : root.helpOpen ? "help" : "main"
    var groups = []
    for (var g in root.expandedGroups) if (root.expandedGroups[g]) groups.push(g)
    return {
      view: view,
      memory: root.memoryOpen ? { projectKey: root.memoryProjectKey, displayName: root.memoryDisplayName,
                                  location: root.memoryLocation, state: memoryPane.snapshot() } : null,
      selectedId: root.selectedId,
      selectedAgent: root.selectedAgent,
      expandedRows: Object.keys(root.expandedRows),
      expandedGroups: groups,
      scrollY: mainScroll.flickable.contentY
    }
  }

  function restore(state) {
    if (!state || typeof state !== "object" || Array.isArray(state)) return
    function text(v, max) { return typeof v === "string" && v.length <= max ? v : "" }
    function keySet(v) {
      var out = {}
      if (!Array.isArray(v)) return out
      for (var i = 0; i < v.length && i < 500; i++) if (text(v[i], 4096) !== "") out[v[i]] = true
      return out
    }
    // Lands in the restored view at once, with no slide to it.
    root.slideAnimated = false
    root.showMain()
    root.expandedRows = keySet(state.expandedRows)
    root.expandedGroups = keySet(state.expandedGroups)
    root.pendingSelection = { id: text(state.selectedId, 8192), agent: text(state.selectedAgent, 256) }
    root.pendingScrollY = typeof state.scrollY === "number" && isFinite(state.scrollY) ? Math.max(0, state.scrollY) : 0
    var m = state.memory
    var memoryKey = m && typeof m === "object" ? text(m.projectKey, 4096) : ""
    if (state.view === "memory" && memoryKey !== "") {
      memoryPane.restore(m.state && typeof m.state === "object" ? m.state : {})
      root.openMemory(memoryKey, text(m.displayName, 1024) || memoryKey, text(m.location, 4096))
    } else if (state.view === "groups") {
      root.groupEditorOpen = true
    } else if (state.view === "help") {
      root.helpOpen = true
    }
    // A Memory still waiting for its records lands when it comes in.
    root.slideAnimated = root.memoryOpen && !root.memoryShowing ? false : true
    if (root.hasLoaded && !root.loading) root.applyPendingSelection()
  }

  // Once a restored Memory that waited has come in, or was left, views
  // slide again, from the next turn so this change itself lands.
  onMemoryShowingChanged: if (!root.slideAnimated) Qt.callLater(root.slideAgain)
  function slideAgain() {
    if (!root.memoryOpen || root.memoryShowing) root.slideAnimated = true
  }

  // Selects the restored row (falling back to the here row when it is gone)
  // and scrolls back to where the snapshot was, once the layout has settled.
  function applyPendingSelection() {
    var want = root.pendingSelection
    root.pendingSelection = null
    var idx = -1
    for (var i = 0; i < root.navRows.length; i++) {
      if (root.rowId(root.navRows[i].summary) === want.id && root.navRows[i].agent === want.agent) { idx = i; break }
    }
    if (idx < 0) root.resetSelection()
    else { root.selectedIndex = idx; root.rememberSelection() }
    var y = root.pendingScrollY
    root.pendingScrollY = 0
    Qt.callLater(function () {
      mainScroll.flickable.contentY = Scroll.clampedY(mainScroll.flickable, y)
    })
  }

  function toggleGroup(group) {
    var next = Object.assign({}, root.expandedGroups)
    next[group] = !next[group]
    root.expandedGroups = next
  }

  // Expands / collapses a folder row's agent sub-rows. Collapsing while a
  // sub-row is selected moves the selection back onto the folder row
  // (indexForKey).
  function setRowExpanded(summary, expanded) {
    var id = root.rowId(summary)
    if (!!root.expandedRows[id] === expanded) return
    if (expanded && Model.summaryAgents(summary).length === 0) return
    var next = Object.assign({}, root.expandedRows)
    if (expanded) next[id] = true
    else delete next[id]
    root.expandedRows = next
  }

  function toggleRow(summary) {
    root.setRowExpanded(summary, !root.expandedRows[root.rowId(summary)])
  }

  function moveSelection(delta) {
    if (root.navRows.length === 0) return
    // Up on the first row scrolls the whole card to the top, so the HERE
    // card and Needs you above Recent are in view again.
    if (delta < 0 && root.selectedIndex === 0) {
      mainScroll.scrollToTop()
      return
    }
    root.selectedIndex = Math.max(0, Math.min(root.navRows.length - 1, root.selectedIndex + delta))
    var row = root.selectedRowIn(recentSection)
    if (row) mainScroll.reveal(row)
  }

  // The visible row (project, group or agent sub-row) whose `selected` is
  // set, so a key move can scroll it into view; null when there is none.
  function selectedRowIn(item) {
    for (var i = 0; i < item.children.length; i++) {
      var child = item.children[i]
      if (!child.visible) continue
      if (child.selected === true) return child
      var found = root.selectedRowIn(child)
      if (found) return found
    }
    return null
  }

  function summaryMemory(summary) {
    root.openMemory(Model.summaryProjectKey(summary), Model.summaryDisplayName(summary), Model.summaryCwd(summary) || "")
  }

  // The content's key handler; returns whether it consumed the key. Esc
  // asks the host to close; o asks it to pop out and p to pin, where it can; u goes back
  // from Groups, Memory or Help to the main body; Memory and Groups take
  // their own keys; the row keys act only on the main body. The text field
  // in Groups keeps the keys it takes: text, and Enter (which adds the
  // group); the rest (Esc, Up and Down) come here.
  function handleKey(key, modifiers) {
    pointerGate.reset()
    if (key === Qt.Key_Escape) { root.closeRequested(); return true }
    if (key === Qt.Key_O && root.canPopOut) { root.popOutRequested(); return true }
    if (key === Qt.Key_P && root.canPin) { root.pinRequested(); return true }
    if (key === Qt.Key_U && (root.groupEditorOpen || root.memoryOpen || root.helpOpen)) { root.showMain(); return true }
    if (root.memoryOpen) return memoryPane.handleKey(key)
    if (root.groupEditorOpen) return groupEditor.handleKey(key)
    if (root.helpOpen) {
      switch (key) {
      case Qt.Key_Question: root.helpOpen = false; return true
      case Qt.Key_J: case Qt.Key_Down: helpPane.scrollBy(Style.space(48)); return true
      case Qt.Key_K: case Qt.Key_Up: helpPane.scrollBy(-Style.space(48)); return true
      }
      return false
    }
    var s = root.selectedSummary
    switch (key) {
    case Qt.Key_J: case Qt.Key_Down: root.moveSelection(1); return true
    case Qt.Key_K: case Qt.Key_Up: root.moveSelection(-1); return true
    case Qt.Key_Right: if (s) root.setRowExpanded(s, true); return true
    case Qt.Key_Left: if (s) root.setRowExpanded(s, false); return true
    case Qt.Key_Return: case Qt.Key_Enter: root.continueOn(s, root.selectedAgent); return true
    case Qt.Key_T: if (s) root.openTerminal(Model.summaryCwd(s)); return true
    case Qt.Key_M: if (s) root.summaryMemory(s); return true
    case Qt.Key_G: root.groupEditorOpen = true; return true
    case Qt.Key_Question: root.helpOpen = true; return true
    case Qt.Key_R: root.refresh(); return true
    }
    return false
  }

  // agent: a sub-row's chosen agent id, "" / absent for a folder row.
  function continueOn(summary, agent) {
    continueAction.continueOn(summary, agent || "")
  }

  function refresh() {
    root.nowMs = Date.now()
    continueAction.refreshDefaultAgent()
    root.loading = true
    dataProcess.running = false
    dataProcess.running = true
  }

  // The ONE place the content reacts to its host showing or hiding,
  // whichever entry point flipped it.
  onActiveChanged: {
    if (root.active) {
      // An opening starts on the here row once its data lands.
      root.selectedKey = null
      root.refresh()
    } else {
      root.showMain()
    }
  }

  // Back from Groups, Memory or Help to the main view.
  function showMain() {
    root.groupEditorOpen = false
    root.memoryOpen = false
    root.helpOpen = false
  }

  // Another folder's Memory asked for while one is open (still waiting for
  // its records, under the main view) opens afresh for the new folder.
  function openMemory(projectKey, displayName, cwd) {
    root.memoryOpen = false
    root.memoryProjectKey = projectKey
    root.memoryDisplayName = displayName
    root.memoryLocation = cwd
    root.memoryOpen = true
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

  // Hover moves the selection only when the pointer really moves, not when
  // rows slide or scroll under a still one (Omarchy's own, as its menu uses).
  PointerMoveGate {
    id: pointerGate
    referenceItem: root
  }

  Timer {
    id: memoryWait
    interval: 300
    onTriggered: root.memoryWaitOver = true
  }

  Process {
    id: dataProcess
    command: Launchers.thisWeekCommand()
    stdout: StdioCollector {
      onStreamFinished: {
        root.loading = false
        root.hasLoaded = true
        pointerGate.reset()
        try {
          root.weekData = JSON.parse(text || "{}")
          // A reload keeps the selected row (onNavRowsChanged).
          if (root.pendingSelection) root.applyPendingSelection()
          else if (root.selectedKey === null) root.resetSelection()
        } catch (e) {
          root.weekData = null
        }
      }
    }
  }

  // The pages lie on top of each other in the space above the footer; each
  // keeps its header in place and scrolls the rest itself (ScrollPage). The
  // tallest one showing (both, while they slide) is the view's height
  // unscrolled.
  Item {
    id: contentColumn
    anchors.left: parent.left
    anchors.right: parent.right
    anchors.top: parent.top
    anchors.bottom: footer.top
    anchors.leftMargin: root.edge
    anchors.rightMargin: root.edge
    anchors.topMargin: root.edge
    anchors.bottomMargin: root.footerGap
    clip: true
    implicitHeight: {
      var h = 0
      for (var i = 0; i < children.length; i++)
        if (children[i].visible) h = Math.max(h, children[i].implicitHeight)
      return h
    }

    SlidePage {
      animate: root.slideAnimated
      objectName: "mainPage"
      current: !root.groupEditorOpen && !root.memoryShowing && !root.helpOpen
      side: -1

      // The header stays put; HERE, Needs you and Recent scroll.
      ScrollPage {
        id: mainScroll
        width: parent.width
        height: parent.height
        spacing: Style.spacing.panelGap

        header: [HeroHeader {
          glyph: root.scrollGlyph
          title: "Backstory"
          status: root.heroStatus
          statusShown: !root.waitingForFirstData
          statusUrgent: root.attentionItems.length > 0

          HostButtons {
            canPopOut: root.canPopOut
            canPin: root.canPin
            pinned: root.pinned
            onPopOutRequested: root.popOutRequested()
            onPinRequested: root.pinRequested()
          }

          PanelActionButton {
            id: groupsButton
            iconText: Glyphs.gear()
            tooltipText: "Groups"
            foreground: Commons.Color.popups.text
            hoverColor: Commons.Color.accent
            onClicked: root.groupEditorOpen = !root.groupEditorOpen
          }

          PanelActionButton {
            id: closeButton
            iconText: Glyphs.close()
            tooltipText: "Close"
            foreground: Commons.Color.popups.text
            hoverColor: Commons.Color.urgent
            onClicked: root.closeRequested()
          }
        }]

        HereCard {
          visible: root.hereProject !== null
          width: parent.width
          summary: root.hereProject
          hereName: root.hereName
          defaultAgent: continueAction.defaultAgent
          continueLabel: continueAction.labelFor(root.hereProject)
          expanded: root.hereProject !== null && !!root.expandedRows[root.rowId(root.hereProject)]
          onToggleExpanded: if (root.hereProject) root.toggleRow(root.hereProject)
          week: root.weekDays
          nowMs: root.nowMs
          onContinueRequested: root.continueOn(root.hereProject)
          onTerminalRequested: root.openTerminal(Model.summaryCwd(root.hereProject))
          onMemoryRequested: root.summaryMemory(root.hereProject)
        }

        // Needs you: rendered only when non-empty (PANEL-CONTRACT.md
        // "Attention", decision 9be5c1d5 — an empty array is itself
        // the "nothing to flag" signal, not an absent section).
        PanelSeparator {
          width: parent.width
          visible: root.attentionItems.length > 0
          foreground: Commons.Color.popups.text
        }

        Column {
          visible: root.attentionItems.length > 0
          width: parent.width
          spacing: Style.spacing.labelGap

          PanelSectionHeader { text: "NEEDS YOU"; foreground: Commons.Color.popups.text }
          AttentionSection { items: root.attentionItems; width: parent.width; onDismissed: root.refresh() }
        }

        PanelSeparator {
          width: parent.width
          foreground: Commons.Color.popups.text
        }

        Column {
          width: parent.width
          spacing: Style.spacing.labelGap

          PanelSectionHeader { text: "RECENT"; foreground: Commons.Color.popups.text }
          WhereLeftOffSection {
            id: recentSection
            rows: root.whereLeftOffRows
            waiting: root.waitingForFirstData
            week: root.weekDays
            expandedGroups: root.expandedGroups
            selectedId: root.selectedId
            selectedAgent: root.selectedAgent
            expandedRows: root.expandedRows
            nowMs: root.nowMs
            onToggleRow: (summary) => root.toggleRow(summary)
            onLaunchRequested: (summary, agent) => root.continueOn(summary, agent)
            width: parent.width
            onToggleGroup: (group) => root.toggleGroup(group)
            onOpenTerminal: (cwd) => root.openTerminal(cwd)
            onContinueRequested: (summary) => root.continueOn(summary)
            onOpenMemory: (projectKey, displayName, cwd) => root.openMemory(projectKey, displayName, cwd)
            onHoverRow: (summary, agent, item, mouse) => { if (pointerGate.moved(item, mouse)) root.selectRow(summary, agent) }
          }
        }

        Text {
          width: parent.width
          textFormat: Text.PlainText
          text: root.summaryText
          visible: !root.waitingForFirstData
          font.family: Style.font.family
          font.pixelSize: Style.font.caption
          color: Qt.darker(Commons.Color.popups.text, 1.3)
          wrapMode: Text.WordWrap
        }

      }
    }

    SlidePage {
      animate: root.slideAnimated
      objectName: "groupsPage"
      current: root.groupEditorOpen

      GroupEditor {
        id: groupEditor
        shown: root.groupEditorOpen
        pointer: pointerGate
        width: parent.width
        height: parent.height
        canPopOut: root.canPopOut
        canPin: root.canPin
        pinned: root.pinned
        onPopOutRequested: root.popOutRequested()
        onPinRequested: root.pinRequested()
        onCloseRequested: root.groupEditorOpen = false
        onChanged: root.refresh()
      }
    }

    SlidePage {
      animate: root.slideAnimated
      objectName: "helpPage"
      current: root.helpOpen

      HelpView {
        id: helpPane
        width: parent.width
        height: parent.height
        canPopOut: root.canPopOut
        canPin: root.canPin
        pinned: root.pinned
        onPopOutRequested: root.popOutRequested()
        onPinRequested: root.pinRequested()
        onCloseRequested: root.helpOpen = false
      }
    }

    SlidePage {
      animate: root.slideAnimated
      objectName: "memoryPage"
      current: root.memoryShowing

      MemoryView {
        id: memoryPane
        shown: root.memoryOpen
        pointer: pointerGate
        width: parent.width
        height: parent.height
        projectKey: root.memoryProjectKey
        displayName: root.memoryDisplayName
        location: root.memoryLocation
        canPopOut: root.canPopOut
        canPin: root.canPin
        pinned: root.pinned
        onPopOutRequested: root.popOutRequested()
        onPinRequested: root.pinRequested()
        onCloseRequested: root.memoryOpen = false
        onChanged: root.refresh()
      }
    }
  }

  // Declared after the pages, so the item tree (and tests reading it)
  // still starts with the content.
  Text {
    id: footer
    objectName: "footerHint"
    anchors.left: parent.left
    anchors.right: parent.right
    anchors.bottom: parent.bottom
    anchors.leftMargin: root.edge
    anchors.rightMargin: root.edge
    anchors.bottomMargin: root.footerBottomMargin
    textFormat: Text.PlainText
    text: root.footerHint
    font.family: Style.font.family
    font.pixelSize: Style.font.caption
    color: Qt.darker(Commons.Color.popups.text, 1.3)
    wrapMode: Text.WordWrap
  }
}
