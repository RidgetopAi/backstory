import QtQuick
import qs.Commons
import "js/model.js" as Model
import "js/glyphs.js" as Glyphs
import "js/format.js" as Format

// One chip per agents[] entry, newest first, each "<name> <relative time>",
// capped at Format.MAX_AGENT_CHIPS with the rest folded into a "+N" chip. A
// click anywhere on the strip toggles the row's per-agent sub-rows.
Item {
  id: root

  property var agents: []
  property bool expanded: false
  property real nowMs: Date.now()

  readonly property var split: Format.chipAgents(root.agents)

  signal toggled()

  visible: root.agents.length > 0
  implicitWidth: strip.implicitWidth
  implicitHeight: strip.implicitHeight

  MouseArea {
    anchors.fill: parent
    cursorShape: Qt.PointingHandCursor
    onClicked: root.toggled()
  }

  Row {
    id: strip
    spacing: Style.space(4)

    Text {
      anchors.verticalCenter: parent.verticalCenter
      textFormat: Text.PlainText
      text: root.expanded ? Glyphs.chevronDown() : Glyphs.chevronRight()
      font.family: Style.font.family
      font.pixelSize: Style.font.caption
      color: Qt.darker(Color.foreground, 1.3)
    }

    Repeater {
      model: root.split.shown

      delegate: Rectangle {
        id: chip
        required property var modelData
        objectName: "agentChip"
        readonly property string agentText: Format.agentName(Model.agentId(modelData))
        readonly property string timeText: Format.relativeTime(Model.agentLastActivity(modelData), root.nowMs)
        anchors.verticalCenter: parent.verticalCenter
        radius: height / 2
        color: Util.alpha(Color.accent, 0.18)
        width: chipRow.implicitWidth + Style.spacing.rowPaddingX
        height: chipRow.implicitHeight + Style.space(2)

        Row {
          id: chipRow
          anchors.centerIn: parent
          spacing: Style.space(3)

          Text {
            objectName: "agentChipName"
            textFormat: Text.PlainText
            text: chip.agentText
            font.family: Style.font.family
            font.pixelSize: Style.font.caption
            color: Color.foreground
          }

          Text {
            textFormat: Text.PlainText
            text: chip.timeText
            font.family: Style.font.family
            font.pixelSize: Style.font.caption
            color: Qt.darker(Color.foreground, 1.3)
          }
        }
      }
    }

    Rectangle {
      visible: root.split.extra > 0
      objectName: "agentChipMore"
      anchors.verticalCenter: parent.verticalCenter
      radius: height / 2
      color: Util.alpha(Color.foreground, 0.12)
      width: moreText.implicitWidth + Style.spacing.rowPaddingX
      height: moreText.implicitHeight + Style.space(2)

      Text {
        id: moreText
        anchors.centerIn: parent
        objectName: "agentChipMoreText"
        textFormat: Text.PlainText
        text: "+" + root.split.extra
        font.family: Style.font.family
        font.pixelSize: Style.font.caption
        color: Color.foreground
      }
    }
  }
}
