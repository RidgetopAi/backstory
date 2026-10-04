import QtQuick
import qs.Ui
import qs.Commons
import "js/model.js" as Model
import "js/format.js" as Format

// A folder row's expanded per-agent sub-rows, one per agents[] entry, newest
// first, indented under the row (the group-children pattern). Continue on a
// sub-row launches THAT agent in the row's cwd.
Column {
  id: root

  property var summary: null
  // The agent id of the keyboard-selected sub-row, "" for none.
  property string selectedAgent: ""
  property real nowMs: Date.now()

  signal launchRequested(string agent)

  width: parent ? parent.width : Style.space(360)
  spacing: 0

  Repeater {
    model: root.summary ? Model.summaryAgents(root.summary) : []

    delegate: Item {
      id: sub
      required property var modelData
      objectName: "agentSubRow"
      readonly property string agentId: Model.agentId(modelData)
      readonly property bool selected: root.selectedAgent === sub.agentId
      readonly property string detail: {
        var parts = [Format.relativeTime(Model.agentLastActivity(modelData), root.nowMs)]
        var n = Model.agentSessionCount(modelData)
        if (n) parts.push(n + (n === 1 ? " session" : " sessions"))
        return parts.filter(function (p) { return p !== "" }).join(" · ")
      }

      width: root.width
      implicitHeight: Math.max(subLabel.implicitHeight, subContinue.implicitHeight) + Style.spacing.rowGap

      Rectangle {
        anchors.fill: parent
        radius: Style.cornerRadius
        color: sub.selected ? Util.alpha(Color.accent, 0.18)
          : (subMouse.containsMouse ? Style.hoverFillFor(Color.foreground, Color.foreground) : "transparent")
        border.width: sub.selected ? 1 : 0
        border.color: Color.accent
      }

      MouseArea {
        id: subMouse
        anchors.fill: parent
        hoverEnabled: true
        cursorShape: Qt.PointingHandCursor
        onClicked: root.launchRequested(sub.agentId)
      }

      Row {
        id: subLabel
        anchors.left: parent.left
        anchors.leftMargin: Style.space(40)
        anchors.verticalCenter: parent.verticalCenter
        spacing: Style.spacing.controlGap

        Text {
          objectName: "agentSubRowName"
          textFormat: Text.PlainText
          text: Format.agentName(sub.agentId)
          font.family: Style.font.family
          font.pixelSize: Style.font.bodySmall
          font.bold: true
          color: Color.foreground
        }

        Text {
          textFormat: Text.PlainText
          text: sub.detail
          font.family: Style.font.family
          font.pixelSize: Style.font.caption
          color: Qt.darker(Color.foreground, 1.3)
          anchors.verticalCenter: parent.verticalCenter
        }
      }

      LabelButton {
        id: subContinue
        objectName: "agentSubRowContinue"
        anchors.right: parent.right
        anchors.verticalCenter: parent.verticalCenter
        label: "Continue"
        onClicked: root.launchRequested(sub.agentId)
      }
    }
  }
}
