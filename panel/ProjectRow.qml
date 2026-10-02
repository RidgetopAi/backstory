import QtQuick
import qs.Ui
import qs.Commons
import "js/model.js" as Model
import "js/derive.js" as Derive
import "js/glyphs.js" as Glyphs
import "js/format.js" as Format

// One project's Recent row: name, relative time, a 7-cell strip of the last
// 7 days shaded by that project's sessions per day (from `week`), and the
// Next line; a workspace-key row reads "<dir> \u00b7 workspace notes" in the
// dim colour. Every field this component reads comes through js/model.js,
// never a bare `.field` on `summary` — see model.js's own doc comment.
Item {
  id: root

  property var summary: null
  property bool indented: false
  property bool selected: false
  property var week: []
  property real nowMs: Date.now()

  readonly property string projectKey: summary ? (Model.summaryProjectKey(summary) || "") : ""
  readonly property bool workspaceRow: Format.isWorkspaceKey(root.projectKey)
  readonly property bool stale: summary ? !!Model.summaryHandoffStale(summary) : false
  readonly property string next: summary ? (Model.summaryHandoffNext(summary) || "") : ""
  readonly property string nameText: {
    var n = summary ? (Model.summaryDisplayName(summary) || "") : ""
    return root.workspaceRow ? n + " \u00b7 workspace notes" : n
  }
  readonly property color nameColor: root.workspaceRow ? Qt.darker(Color.foreground, 1.3) : Color.foreground
  readonly property var stripDays: Format.stripDays(root.nowMs)
  readonly property var stripCounts: {
    var out = []
    for (var i = 0; i < root.stripDays.length; i++) out.push(Derive.sessionsOnDay(root.week, root.projectKey, root.stripDays[i]))
    return out
  }
  readonly property int stripMax: Math.max.apply(null, root.stripCounts.concat([0]))

  // Click anywhere on the row (outside the buttons) opens a terminal at
  // this project's cwd (AGENT-CONTRACT.md "same-agent resume is reopening
  // a terminal at the session's cwd"); the buttons are Continue and Memory.
  signal openTerminal(string cwd)
  signal continueRequested()
  signal openMemory(string projectKey, string displayName, string cwd)

  implicitHeight: column.implicitHeight + Style.spacing.rowGap
  implicitWidth: parent ? parent.width : Style.space(360)

  Rectangle {
    anchors.fill: parent
    radius: Style.cornerRadius
    color: root.selected ? Util.alpha(Color.accent, 0.18)
      : (rowMouse.containsMouse ? Style.hoverFillFor(Color.foreground, Color.foreground) : "transparent")
    border.width: root.selected ? 1 : 0
    border.color: Color.accent
  }

  MouseArea {
    id: rowMouse
    anchors.fill: parent
    hoverEnabled: true
    cursorShape: Qt.PointingHandCursor
    onClicked: root.openTerminal(Model.summaryCwd(root.summary))
  }

  Column {
    id: column
    anchors.left: parent.left
    anchors.right: continueButton.left
    anchors.leftMargin: root.indented ? Style.space(20) : Style.spacing.rowPaddingX
    anchors.rightMargin: Style.spacing.rowPaddingX
    anchors.verticalCenter: parent.verticalCenter
    spacing: Style.spacing.labelGap / 2

    Row {
      width: parent.width
      spacing: Style.spacing.controlGap

      Text {
        textFormat: Text.PlainText
        text: root.stale ? Glyphs.attention() : Glyphs.terminal()
        font.family: Style.font.family
        font.pixelSize: Style.font.bodySmall
        color: root.stale ? Color.urgent : Color.foreground
      }

      Text {
        objectName: "rowName"
        textFormat: Text.PlainText
        text: root.nameText
        font.family: Style.font.family
        font.pixelSize: Style.font.body
        font.bold: !root.workspaceRow
        color: root.nameColor
        elide: Text.ElideRight
        width: Math.min(implicitWidth, parent.width - Style.space(72))
      }

      Text {
        textFormat: Text.PlainText
        text: root.summary ? Format.relativeTime(Model.summaryLastActivity(root.summary), root.nowMs) : ""
        font.family: Style.font.family
        font.pixelSize: Style.font.caption
        color: Qt.darker(Color.foreground, 1.3)
        anchors.verticalCenter: parent.verticalCenter
      }
    }

    Row {
      spacing: Style.space(2)

      Repeater {
        model: root.stripCounts

        delegate: Rectangle {
          required property var modelData
          objectName: "stripCell"
          readonly property bool shaded: modelData > 0
          readonly property real strength: Format.stripAlpha(modelData, root.stripMax)
          width: Style.space(10)
          height: Style.space(6)
          radius: Style.space(2)
          color: shaded ? Util.alpha(Color.accent, strength) : Util.alpha(Color.foreground, 0.12)
        }
      }
    }

    Text {
      visible: root.next !== ""
      textFormat: Text.PlainText
      text: root.next
      font.family: Style.font.family
      font.pixelSize: Style.font.caption
      color: Qt.darker(Color.foreground, 1.3)
      elide: Text.ElideRight
      width: parent.width
    }
  }

  PanelActionButton {
    id: memoryButton
    anchors.right: parent.right
    anchors.verticalCenter: parent.verticalCenter
    iconText: Glyphs.memory()
    tooltipText: "Memory"
    foreground: Color.foreground
    hoverColor: Color.accent
    onClicked: root.openMemory(Model.summaryProjectKey(root.summary), Model.summaryDisplayName(root.summary), Model.summaryCwd(root.summary) || "")
  }

  PanelActionButton {
    id: continueButton
    anchors.right: memoryButton.left
    anchors.verticalCenter: parent.verticalCenter
    iconText: Glyphs.resume()
    tooltipText: "Continue"
    foreground: Color.foreground
    hoverColor: Color.accent
    onClicked: root.continueRequested()
  }
}
