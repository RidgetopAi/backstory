import QtQuick
import Quickshell
import Quickshell.Io
import qs.Ui
import qs.Commons
import "js/launchers.js" as Launchers
import "js/records.js" as Records
import "js/memory.js" as Memory
import "js/glyphs.js" as Glyphs

// Memory view (decision 02c511b3 D5): one row's saved records — see,
// edit, delete — plus "Forget activity and saved records". A CLI CLIENT like the rest of the
// panel: it runs `backstory records|edit|delete|purge` and reads stdout,
// never the store. Swapped in for the This Week body by Panel.qml (same
// pattern as GroupEditor). Refreshes whenever it becomes visible, so a
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
  // Asks the scroll view to show `target` (the confirm, note or error a
  // click just produced), so nobody scrolls to find the result of a click.
  signal reveal(Item target)

  property var payload: ({})
  property bool showHistory: false
  property string errorText: ""
  property bool busy: false

  // id of the record whose inline "Forget this record?" confirm is open.
  property string confirmingId: ""
  // The pending purge scope after a successful dry-run: { label, since,
  // sessions, events }, or null.
  property var pendingPurge: null
  property string purgeNote: ""

  // The clock, injectable so a test can pin the RFC3339 bound it asserts.
  property var now: function () { return Date.now() }

  readonly property var records: Records.topRecords(root.payload)

  function refresh() {
    if (root.projectKey === "") return
    listProcess.command = Launchers.recordsCommand(root.projectKey, root.location, root.showHistory)
    listProcess.running = false
    listProcess.running = true
  }

  onVisibleChanged: {
    if (root.visible) {
      root.errorText = ""
      root.confirmingId = ""
      root.pendingPurge = null
      root.purgeNote = ""
      root.refresh()
    }
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
      root.reveal(target)
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
    stdout: StdioCollector {
      onStreamFinished: {
        try {
          root.payload = JSON.parse(text || "{}")
        } catch (e) {
          root.payload = ({})
        }
      }
    }
    stderr: StdioCollector { id: listStderr }
    onExited: (exitCode, exitStatus) => {
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
  implicitHeight: content.implicitHeight

  Column {
    id: content
    width: parent.width
    spacing: Style.spacing.rowGap

    Row {
      width: parent.width

      PanelActionButton {
        iconText: Glyphs.back()
        tooltipText: "Back"
        foreground: Color.popups.text
        hoverColor: Color.accent
        onClicked: root.closeRequested()
      }

      Text {
        textFormat: Text.PlainText
        text: "Memory · " + root.displayName
        font.family: Style.font.family
        font.pixelSize: Style.font.heading
        font.bold: true
        color: Color.popups.text
        elide: Text.ElideRight
        width: parent.width - Style.space(56)
        anchors.verticalCenter: parent.verticalCenter
      }

      PanelActionButton {
        iconText: Glyphs.history()
        tooltipText: root.showHistory ? "Hide history" : "Show history"
        foreground: root.showHistory ? Color.accent : Color.popups.text
        hoverColor: Color.accent
        onClicked: root.toggleHistory()
      }
    }

    Text {
      id: errorLabel
      visible: root.errorText !== ""
      textFormat: Text.PlainText
      text: root.errorText
      wrapMode: Text.WordWrap
      width: parent.width
      font.family: Style.font.family
      font.pixelSize: Style.font.caption
      color: Color.urgent
    }

    Text {
      visible: root.records.length === 0
      textFormat: Text.PlainText
      text: "Nothing saved for " + root.displayName + " yet."
      wrapMode: Text.WordWrap
      width: parent.width
      font.family: Style.font.family
      font.pixelSize: Style.font.bodySmall
      color: Qt.darker(Color.foreground, 1.3)
      leftPadding: Style.spacing.rowPaddingX
    }

    Repeater {
      id: recordRepeater
      model: root.records

      delegate: Item {
        id: rec
        required property var modelData
        readonly property string rid: Records.recordId(modelData)
        readonly property string mark: Memory.statusMark(Records.recordStatus(modelData))
        readonly property bool deleted: Records.recordStatus(modelData) === "deleted"
        readonly property string body: Records.recordText(modelData)
        readonly property Item confirmFlow: recordConfirm
        property bool expanded: false

        width: content.width
        height: recColumn.implicitHeight + Style.spacing.rowGap

        MouseArea {
          anchors.fill: parent
          cursorShape: Qt.PointingHandCursor
          onClicked: rec.expanded = !rec.expanded
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
                color: Color.foreground
              }

              Text {
                textFormat: Text.PlainText
                text: Memory.kindLabel(Records.recordKind(rec.modelData))
                font.family: Style.font.family
                font.pixelSize: Style.font.caption
                font.bold: true
                color: Color.foreground
              }

              Text {
                textFormat: Text.PlainText
                text: Memory.tierLabel(Records.recordTier(rec.modelData))
                font.family: Style.font.family
                font.pixelSize: Style.font.caption
                font.bold: true
                color: Color.foreground
              }

              Text {
                visible: rec.mark !== ""
                textFormat: Text.PlainText
                text: rec.mark
                font.family: Style.font.family
                font.pixelSize: Style.font.caption
                color: Color.urgent
              }

              Text {
                textFormat: Text.PlainText
                text: Memory.relativeAge(Records.recordTs(rec.modelData), root.now())
                font.family: Style.font.family
                font.pixelSize: Style.font.caption
                color: Qt.darker(Color.foreground, 1.3)
              }
            }

            Row {
              anchors.right: parent.right
              anchors.verticalCenter: parent.verticalCenter
              visible: !rec.deleted

              PanelActionButton {
                iconText: Glyphs.edit()
                tooltipText: "Edit"
                foreground: Color.foreground
                hoverColor: Color.accent
                onClicked: root.editRecord(rec.rid)
              }

              PanelActionButton {
                iconText: Glyphs.trash()
                tooltipText: "Delete"
                foreground: Color.foreground
                hoverColor: Color.urgent
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
            color: Color.foreground
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
              color: Color.urgent
            }

            MemoryButton {
              label: "Forget"
              foreground: Color.urgent
              hoverColor: Color.urgent
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

    Text {
      textFormat: Text.PlainText
      text: "Forget activity and saved records"
      font.family: Style.font.family
      font.pixelSize: Style.font.caption
      font.bold: true
      color: Qt.darker(Color.foreground, 1.3)
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
      visible: root.purgeNote !== ""
      textFormat: Text.PlainText
      text: root.purgeNote
      wrapMode: Text.WordWrap
      width: parent.width
      font.family: Style.font.family
      font.pixelSize: Style.font.caption
      color: Qt.darker(Color.foreground, 1.3)
    }

    Column {
      id: purgeConfirm
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
        color: Color.urgent
      }

      Row {
        spacing: Style.spacing.controlGap

        MemoryButton {
          label: "Forget"
          foreground: Color.urgent
          hoverColor: Color.urgent
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
