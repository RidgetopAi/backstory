import QtQuick
import Quickshell
import Quickshell.Io
import qs.Ui
import qs.Commons
import qs.Commons as Commons
import "js/launchers.js" as Launchers
import "js/records.js" as Records
import "js/memory.js" as Memory
import "js/glyphs.js" as Glyphs

// Memory view (decision 02c511b3 D5): one row's saved records — see,
// edit, delete — plus "Forget activity and saved records". A CLI CLIENT like the rest of the
// panel: it runs `backstory records|edit|delete|purge` and reads stdout,
// never the store. Swapped in for the This Week body by Panel.qml (same
// pattern as GroupEditor). Refreshes whenever it is opened (shown), so a
// record edited in a terminal shows up on the next open (no polling).
Item {
  id: root

  property string projectKey: ""
  property string displayName: ""
  // The row's own cwd (its This Week summary cwd): every records / purge run
  // is scoped to this row with --location, never to the shared project key.
  property string location: ""

  signal changed()
  signal closeRequested()
  // Where the view is shown: the bar popup sets canPopOut, the window
  // canPin and pinned; the header then offers those buttons (HostButtons).
  property bool canPopOut: false
  property bool canPin: false
  property bool pinned: false
  signal popOutRequested()
  signal pinRequested()
  // The scrolling part under the header (ScrollPage), for the host and tests.
  readonly property alias flickable: page.flickable

  property var payload: ({})
  property bool showHistory: false
  // Which records show their full text, keyed by record id. Opening the view
  // again for the same project keeps them and the highlight; another project
  // starts with none expanded and the first record highlighted; a snapshot
  // handed to restore() (the bar popup's pop-out) sets pendingExpanded,
  // which the next opening takes instead.
  property var expandedIds: ({})
  property var pendingExpanded: null
  // The project and folder last shown: records are kept per folder
  // (--location), and two folders can share a project key.
  property string shownScope: ""
  property string errorText: ""
  property bool busy: false
  // The one highlighted record, which the keys act on and hover moves.
  property int cursorIndex: 0
  // The host's gate: hover moves the highlight only on a real pointer move.
  property PointerMoveGate pointer: null

  // id of the record whose inline "Forget this record?" confirm is open.
  property string confirmingId: ""
  // The pending purge scope after a successful dry-run: { label, since,
  // sessions, events }, or null.
  property var pendingPurge: null
  property string purgeNote: ""

  // The clock, injectable so a test can pin the RFC3339 bound it asserts.
  property var now: function () { return Date.now() }

  readonly property var records: Records.topRecords(root.payload)
  // Fewer records than before (one deleted, history hidden) keep a highlight.
  onRecordsChanged: if (root.cursorIndex >= root.records.length) root.cursorIndex = Math.max(0, root.records.length - 1)

  // The folder the records on show belong to; the host slides Memory in
  // once they are this folder's, so it never shows another's or an empty
  // page that is only still loading.
  readonly property string scope: root.projectKey + "\n" + root.location
  property string loadedScope: ""
  property string requestScope: ""
  readonly property bool ready: root.loadedScope === root.scope

  function refresh() {
    if (root.projectKey === "") return
    root.requestScope = root.scope
    // A run still going is stopped, and ends (a crash, with what it wrote
    // so far) before the new one starts: that end is not this request's.
    if (listProcess.running) listProcess.superseded = true
    listProcess.command = Launchers.recordsCommand(root.projectKey, root.location, root.showHistory)
    listProcess.running = false
    listProcess.running = true
  }

  // Set while Memory is the view showing. Opening runs on this, not on
  // visible: the page stays visible while it slides out, so going back in
  // during the slide would not count as opening it.
  property bool shown: false

  onShownChanged: {
    if (root.shown) {
      if (root.pendingExpanded !== null) {
        root.expandedIds = root.pendingExpanded
        root.pendingExpanded = null
        root.cursorIndex = 0
      } else if (root.projectKey + "\n" + root.location !== root.shownScope) {
        root.expandedIds = {}
        root.cursorIndex = 0
        page.scrollToTop()
      }
      root.shownScope = root.projectKey + "\n" + root.location
      if (!root.ready) root.payload = ({})
      root.errorText = ""
      root.confirmingId = ""
      root.pendingPurge = null
      root.purgeNote = ""
      root.refresh()
    }
  }

  function setExpanded(recordId, expanded) {
    if (!!root.expandedIds[recordId] !== expanded) root.toggleExpanded(recordId)
  }

  // j/k and the arrows move the highlight, as on the main view: up from the
  // first record shows the header again. Right expands the highlighted
  // record, Left folds it, Enter toggles it. Returns whether it used the key.
  function handleKey(key) {
    var rec = recordRepeater.itemAt(root.cursorIndex)
    switch (key) {
    case Qt.Key_J: case Qt.Key_Down: root.moveCursor(1); return true
    case Qt.Key_K: case Qt.Key_Up: root.moveCursor(-1); return true
    case Qt.Key_Right: if (rec) root.setExpanded(rec.rid, true); return true
    case Qt.Key_Left: if (rec) root.setExpanded(rec.rid, false); return true
    case Qt.Key_Return: case Qt.Key_Enter: if (rec) root.toggleExpanded(rec.rid); return true
    }
    return false
  }

  function moveCursor(delta) {
    if (root.records.length === 0) return
    if (delta < 0 && root.cursorIndex === 0) { page.scrollToTop(); return }
    root.cursorIndex = Math.max(0, Math.min(root.records.length - 1, root.cursorIndex + delta))
    var rec = recordRepeater.itemAt(root.cursorIndex)
    if (rec) page.reveal(rec)
  }

  function toggleExpanded(recordId) {
    var next = Object.assign({}, root.expandedIds)
    if (next[recordId]) delete next[recordId]
    else next[recordId] = true
    root.expandedIds = next
  }

  // What the view shows beyond its project: the history toggle and the
  // expanded records. restore() takes effect when the view next opens.
  function snapshot() {
    return { history: root.showHistory, expanded: Object.keys(root.expandedIds) }
  }

  function restore(state) {
    root.showHistory = state.history === true
    var ids = {}
    var list = Array.isArray(state.expanded) ? state.expanded : []
    for (var i = 0; i < list.length; i++) if (typeof list[i] === "string") ids[list[i]] = true
    root.pendingExpanded = ids
  }

  function toggleHistory() {
    root.showHistory = !root.showHistory
    root.refresh()
  }

  function editRecord(recordId) {
    Quickshell.execDetached(Launchers.editCommand(recordId))
  }

  // Scrolls the item a state change just made visible into view. Deferred
  // one turn so the layout has seen the new state; `kind` names the target.
  function revealSoon(kind) {
    Qt.callLater(function () { root.revealNow(kind) })
  }

  function revealNow(kind) {
    var target = null
    if (kind === "error") target = errorLabel
    else if (kind === "note") target = noteLabel
    else if (kind === "purge") target = purgeConfirm
    else if (kind === "record") {
      for (var i = 0; i < recordRepeater.count; i++) {
        var row = recordRepeater.itemAt(i)
        if (row && row.rid === root.confirmingId) target = row.confirmFlow
      }
    }
    if (target && target.visible) {
      // Lay out innermost-first so the target's own position is final.
      for (var p = target; p; p = p.parent) {
        if (typeof p.forceLayout === "function") p.forceLayout()
      }
      page.reveal(target)
    }
  }

  // Every error path shows the message and brings it into view.
  function fail(message) {
    root.errorText = message
    root.revealSoon("error")
  }

  function askDelete(recordId) {
    root.pendingPurge = null
    root.purgeNote = ""
    root.confirmingId = recordId
    root.revealSoon("record")
  }

  function deleteRecord(recordId) {
    root.errorText = ""
    root.confirmingId = ""
    mutateProcess.command = Launchers.deleteCommand(recordId)
    root.busy = true
    mutateProcess.running = true
  }

  // Forget activity, step 1: preview with --dry-run; nothing changes yet.
  function previewPurge(scope) {
    root.errorText = ""
    root.confirmingId = ""
    root.pendingPurge = null
    root.purgeNote = ""
    purgeProcess.dryRun = true
    purgeProcess.scope = { label: scope.label, since: scope.since(root.now()) }
    purgeProcess.command = Launchers.purgeCommand(root.projectKey, root.location, purgeProcess.scope.since, true)
    root.busy = true
    purgeProcess.running = true
  }

  // Step 2, only after the human confirms: the same argv with --yes.
  function confirmPurge() {
    var scope = root.pendingPurge
    if (!scope) return
    root.pendingPurge = null
    purgeProcess.dryRun = false
    purgeProcess.scope = scope
    purgeProcess.command = Launchers.purgeCommand(root.projectKey, root.location, scope.since, false)
    root.busy = true
    purgeProcess.running = true
  }

  function cancelPurge() {
    root.pendingPurge = null
  }

  Process {
    id: listProcess
    // Set while the run ending next is one refresh() stopped.
    property bool superseded: false
    stdout: StdioCollector {
      onStreamFinished: {
        if (listProcess.superseded) return
        // New records are a list change, not a hover: the pointer may have
        // moved over the loading page, and they come in under it.
        if (root.pointer) root.pointer.reset()
        try {
          root.payload = JSON.parse(text || "{}")
        } catch (e) {
          root.payload = ({})
        }
        root.loadedScope = root.requestScope
      }
    }
    stderr: StdioCollector { id: listStderr }
    onExited: (exitCode, exitStatus) => {
      if (listProcess.superseded) {
        listProcess.superseded = false
        return
      }
      root.loadedScope = root.requestScope
      if (exitCode !== 0) {
        root.fail(listStderr.text.length > 0 ? listStderr.text : ("backstory records exited " + exitCode))
      }
    }
  }

  Process {
    id: mutateProcess
    stderr: StdioCollector { id: mutateStderr }
    onExited: (exitCode, exitStatus) => {
      root.busy = false
      if (exitCode === 0) {
        root.refresh()
      } else {
        root.fail(mutateStderr.text.length > 0 ? mutateStderr.text : ("backstory delete exited " + exitCode))
      }
    }
  }

  Process {
    id: purgeProcess
    property bool dryRun: true
    property var scope: null
    stdout: StdioCollector { id: purgeStdout }
    stderr: StdioCollector { id: purgeStderr }
    onExited: (exitCode, exitStatus) => {
      root.busy = false
      if (exitCode !== 0) {
        root.fail(purgeStderr.text.length > 0 ? purgeStderr.text : ("backstory purge exited " + exitCode))
        return
      }
      if (purgeProcess.dryRun) {
        var counts = Memory.parseDryRun(purgeStdout.text)
        if (counts === null) {
          root.fail("Unexpected output from backstory purge --dry-run")
        } else if (Memory.nothingToForget(counts)) {
          root.purgeNote = "Nothing to forget for " + purgeProcess.scope.label.toLowerCase() + "."
          root.revealSoon("note")
        } else {
          root.pendingPurge = { label: purgeProcess.scope.label, since: purgeProcess.scope.since, sessions: counts.sessions, events: counts.events, records: counts.records }
          root.revealSoon("purge")
        }
      } else {
        root.purgeNote = ""
        root.refresh()
        root.changed()
      }
    }
  }

  implicitWidth: Style.space(360)
  implicitHeight: page.implicitHeight

  // The header and the rule under it stay put; the records and Forget scroll.
  ScrollPage {
    id: page
    anchors.fill: parent
    spacing: Style.spacing.rowGap

    header: [HeroHeader {
      id: header
      showBack: true
      title: root.displayName
      status: !root.ready ? "Memory"
        : "Memory · " + root.records.length + (root.records.length === 1 ? " saved record" : " saved records")
      onBack: root.closeRequested()

      HostButtons {
        canPopOut: root.canPopOut
        canPin: root.canPin
        pinned: root.pinned
        onPopOutRequested: root.popOutRequested()
        onPinRequested: root.pinRequested()
      }

      PanelActionButton {
        iconText: Glyphs.history()
        tooltipText: root.showHistory ? "Hide history" : "Show history"
        foreground: root.showHistory ? Commons.Color.accent : Commons.Color.popups.text
        hoverColor: Commons.Color.accent
        onClicked: root.toggleHistory()
      }
    },

    PanelSeparator { width: parent.width; foreground: Commons.Color.popups.text }]

    Text {
      id: errorLabel
      visible: root.errorText !== ""
      textFormat: Text.PlainText
      text: root.errorText
      wrapMode: Text.WordWrap
      width: parent.width
      font.family: Style.font.family
      font.pixelSize: Style.font.caption
      color: Commons.Color.urgent
    }

    Text {
      visible: !root.ready
      textFormat: Text.PlainText
      text: "Loading records\u2026"
      width: parent.width
      font.family: Style.font.family
      font.pixelSize: Style.font.bodySmall
      color: Qt.darker(Commons.Color.foreground, 1.3)
      leftPadding: Style.spacing.rowPaddingX
    }

    Text {
      visible: root.ready && root.records.length === 0
      textFormat: Text.PlainText
      text: "Nothing saved for " + root.displayName + " yet."
      wrapMode: Text.WordWrap
      width: parent.width
      font.family: Style.font.family
      font.pixelSize: Style.font.bodySmall
      color: Qt.darker(Commons.Color.foreground, 1.3)
      leftPadding: Style.spacing.rowPaddingX
    }

    Repeater {
      id: recordRepeater
      model: root.records

      delegate: Item {
        id: rec
        required property var modelData
        required property int index
        readonly property string rid: Records.recordId(modelData)
        readonly property string mark: Memory.statusMark(Records.recordStatus(modelData))
        readonly property bool deleted: Records.recordStatus(modelData) === "deleted"
        readonly property string body: Records.recordText(modelData)
        readonly property Item confirmFlow: recordConfirm
        readonly property bool expanded: !!root.expandedIds[rec.rid]
        readonly property bool current: rec.index === root.cursorIndex

        width: page.width
        height: recColumn.implicitHeight + Style.spacing.rowGap

        // Drawn like the main view's selected row (ProjectRow.qml).
        Rectangle {
          anchors.fill: parent
          radius: Style.cornerRadius
          color: rec.current ? Util.alpha(Commons.Color.accent, 0.18) : "transparent"
          border.width: rec.current ? 1 : 0
          border.color: Commons.Color.accent
        }

        MouseArea {
          id: recMouse
          anchors.fill: parent
          hoverEnabled: true
          cursorShape: Qt.PointingHandCursor
          onPositionChanged: (mouse) => { if (root.pointer.moved(recMouse, mouse)) root.cursorIndex = rec.index }
          onClicked: root.toggleExpanded(rec.rid)
        }

        Column {
          id: recColumn
          anchors.left: parent.left
          anchors.right: parent.right
          anchors.leftMargin: Style.spacing.rowPaddingX
          anchors.rightMargin: Style.spacing.rowPaddingX
          anchors.verticalCenter: parent.verticalCenter
          spacing: Style.spacing.labelGap / 2

          Item {
            width: parent.width
            height: Math.max(infoRow.implicitHeight, Style.space(24))

            Row {
              id: infoRow
              anchors.left: parent.left
              anchors.verticalCenter: parent.verticalCenter
              spacing: Style.spacing.controlGap

              Text {
                textFormat: Text.PlainText
                text: Glyphs.recordKind(Records.recordKind(rec.modelData))
                font.family: Style.font.family
                font.pixelSize: Style.font.bodySmall
                color: Commons.Color.foreground
              }

              Text {
                textFormat: Text.PlainText
                text: Memory.kindLabel(Records.recordKind(rec.modelData))
                font.family: Style.font.family
                font.pixelSize: Style.font.caption
                font.bold: true
                color: Commons.Color.foreground
              }

              Text {
                textFormat: Text.PlainText
                text: Memory.tierLabel(Records.recordTier(rec.modelData))
                font.family: Style.font.family
                font.pixelSize: Style.font.caption
                font.bold: true
                color: Commons.Color.foreground
              }

              DeviceChip {
                objectName: "recordDeviceChip"
                device: Records.recordOriginDevice(rec.modelData)
              }

              Text {
                visible: rec.mark !== ""
                textFormat: Text.PlainText
                text: rec.mark
                font.family: Style.font.family
                font.pixelSize: Style.font.caption
                color: Commons.Color.urgent
              }

              Text {
                textFormat: Text.PlainText
                text: Memory.relativeAge(Records.recordTs(rec.modelData), root.now())
                font.family: Style.font.family
                font.pixelSize: Style.font.caption
                color: Qt.darker(Commons.Color.foreground, 1.3)
              }
            }

            Row {
              anchors.right: parent.right
              anchors.verticalCenter: parent.verticalCenter
              visible: !rec.deleted

              PanelActionButton {
                iconText: Glyphs.edit()
                tooltipText: "Edit"
                foreground: Commons.Color.foreground
                hoverColor: Commons.Color.accent
                onClicked: root.editRecord(rec.rid)
              }

              PanelActionButton {
                iconText: Glyphs.trash()
                tooltipText: "Delete"
                foreground: Commons.Color.foreground
                hoverColor: Commons.Color.urgent
                enabled: !root.busy
                onClicked: root.askDelete(rec.rid)
              }
            }
          }

          Text {
            visible: !rec.deleted
            textFormat: Text.PlainText
            text: rec.expanded ? rec.body : Memory.firstLine(rec.body)
            wrapMode: Text.WordWrap
            maximumLineCount: rec.expanded ? 1000 : 1
            elide: rec.expanded ? Text.ElideNone : Text.ElideRight
            width: parent.width
            font.family: Style.font.family
            font.pixelSize: Style.font.body
            color: Commons.Color.foreground
          }

          Flow {
            id: recordConfirm
            visible: root.confirmingId === rec.rid
            width: parent.width
            spacing: Style.spacing.controlGap

            Text {
              textFormat: Text.PlainText
              text: "Forget this record?"
              font.family: Style.font.family
              font.pixelSize: Style.font.caption
              color: Commons.Color.urgent
            }

            MemoryButton {
              label: "Forget"
              foreground: Commons.Color.urgent
              hoverColor: Commons.Color.urgent
              onClicked: root.deleteRecord(rec.rid)
            }

            MemoryButton {
              label: "Cancel"
              onClicked: root.confirmingId = ""
            }
          }
        }
      }
    }

    PanelSeparator {
      objectName: "forgetDivider"
      width: parent.width
      foreground: Commons.Color.popups.text
    }

    Column {
      id: forgetSection
      objectName: "forgetSection"
      width: parent.width
      topPadding: Style.spacing.panelGap
      spacing: Style.spacing.labelGap

      Text {
        textFormat: Text.PlainText
        objectName: "forgetHeading"
        text: "Forget activity and saved records"
        wrapMode: Text.WordWrap
        width: parent.width
        font.family: Style.font.family
        font.pixelSize: Style.font.body
        font.bold: true
        color: Commons.Color.foreground
      }

      Text {
        objectName: "forgetHelper"
        textFormat: Text.PlainText
        text: "Erases captured activity and saved records for this project."
        wrapMode: Text.WordWrap
        width: parent.width
        font.family: Style.font.family
        font.pixelSize: Style.font.caption
        color: Qt.darker(Commons.Color.foreground, 1.3)
      }

      Flow {
        width: parent.width
        spacing: Style.spacing.controlGap

        Repeater {
          model: Memory.FORGET_SCOPES
          delegate: MemoryButton {
            required property var modelData
            label: modelData.label
            enabled: !root.busy
            onClicked: root.previewPurge(modelData)
          }
        }
      }

      Text {
        id: noteLabel
        objectName: "forgetNote"
        visible: root.purgeNote !== ""
        textFormat: Text.PlainText
        text: root.purgeNote
        wrapMode: Text.WordWrap
        width: parent.width
        font.family: Style.font.family
        font.pixelSize: Style.font.caption
        color: Qt.darker(Commons.Color.foreground, 1.3)
      }

      Column {
        id: purgeConfirm
        objectName: "forgetConfirm"
        visible: root.pendingPurge !== null
        width: parent.width
        spacing: Style.spacing.labelGap

        Text {
          textFormat: Text.PlainText
          text: root.pendingPurge
            ? Memory.forgetConfirmText(root.pendingPurge, root.displayName)
            : ""
          wrapMode: Text.WordWrap
          width: parent.width
          font.family: Style.font.family
          font.pixelSize: Style.font.caption
          color: Commons.Color.urgent
        }

        Row {
          spacing: Style.spacing.controlGap

          MemoryButton {
            label: "Forget"
            foreground: Commons.Color.urgent
            hoverColor: Commons.Color.urgent
            onClicked: root.confirmPurge()
          }

          MemoryButton {
            label: "Cancel"
            onClicked: root.cancelPurge()
          }
        }
      }
    }
  }
}
