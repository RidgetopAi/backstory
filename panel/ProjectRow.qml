import QtQuick
import qs.Ui
import qs.Commons
import "js/model.js" as Model
import "js/glyphs.js" as Glyphs

// One project's Where-you-left-off row: a standalone row, or one entry in
// a group row's children (PANEL-CONTRACT.md "Where you left off"). Every
// field this component reads comes through js/model.js, never a bare
// `.field` on `summary` — see model.js's own doc comment for why.
Item {
  id: root

  property var summary: null
  property bool indented: false
  readonly property string handoffId: summary ? (Model.summaryHandoffId(summary) || "") : ""
  readonly property bool hasHandoff: handoffId !== ""
  readonly property bool stale: summary ? !!Model.summaryHandoffStale(summary) : false

  // Click anywhere on the row (outside the resume button) opens a terminal
  // at this project's cwd; "Resume in <agent>" is the row's one other
  // action (AGENT-CONTRACT.md "same-agent resume is reopening a terminal
  // at the session's cwd" / "Resume in <agent>").
  signal openTerminal(string cwd)
  signal resumeAgent(string cwd, string handoffText, string handoffId)
  signal openMemory(string projectKey, string displayName)

  implicitHeight: column.implicitHeight + Style.spacing.rowGap
  implicitWidth: parent ? parent.width : Style.space(360)

  Rectangle {
    anchors.fill: parent
    radius: Style.cornerRadius
    color: rowMouse.containsMouse ? Style.hoverFillFor(Color.foreground, Color.foreground) : "transparent"
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
    anchors.right: resumeButton.visible ? resumeButton.left : memoryButton.left
    anchors.leftMargin: root.indented ? Style.space(20) : Style.spacing.rowPaddingX
    anchors.rightMargin: Style.spacing.rowPaddingX
    anchors.verticalCenter: parent.verticalCenter
    spacing: Style.spacing.labelGap / 2

    Row {
      spacing: Style.spacing.controlGap

      Text {
        textFormat: Text.PlainText
        text: root.stale ? Glyphs.attention() : Glyphs.terminal()
        font.family: Style.font.family
        font.pixelSize: Style.font.bodySmall
        color: root.stale ? Color.urgent : Color.foreground
      }

      Text {
        textFormat: Text.PlainText
        text: root.summary ? Model.summaryDisplayName(root.summary) : ""
        font.family: Style.font.family
        font.pixelSize: Style.font.body
        font.bold: true
        color: Color.foreground
        elide: Text.ElideRight
      }
    }

    Text {
      visible: root.hasHandoff
      textFormat: Text.PlainText
      text: root.summary ? (Model.summaryHandoffFirstLine(root.summary) || "") : ""
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
    onClicked: root.openMemory(Model.summaryProjectKey(root.summary), Model.summaryDisplayName(root.summary))
  }

  PanelActionButton {
    id: resumeButton
    visible: root.hasHandoff
    anchors.right: memoryButton.left
    anchors.verticalCenter: parent.verticalCenter
    iconText: Glyphs.resume()
    tooltipText: "Resume in agent"
    foreground: Color.foreground
    hoverColor: Color.accent
    onClicked: root.resumeAgent(
      Model.summaryCwd(root.summary),
      root.summary ? (Model.summaryHandoffFirstLine(root.summary) || "") : "",
      root.handoffId)
  }
}
