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

  // The width the strip may use; a parent that bounds the strip sets this so
  // chips that do not fit fold into the "+N" chip.
  property real availableWidth: Infinity

  readonly property var split: Format.chipAgents(root.agents)
  property var chipWidths: []
  readonly property int fitCount: Format.fitChipCount(root.chipWidths, root.agents.length,
    root.availableWidth, chevron.implicitWidth, moreMeasure.implicitWidth + Style.spacing.rowPaddingX, strip.spacing)
  readonly property int foldedCount: root.agents.length - Math.min(root.fitCount, root.split.shown.length)

  function setChipWidth(i, w) {
    var next = root.chipWidths.slice()
    next[i] = w
    root.chipWidths = next
  }

  signal toggled()

  visible: root.agents.length > 0
  implicitWidth: strip.implicitWidth
  width: Math.min(implicitWidth, availableWidth)
  clip: true
  implicitHeight: strip.implicitHeight

  MouseArea {
    anchors.fill: parent
    cursorShape: Qt.PointingHandCursor
    onClicked: root.toggled()
  }

  Text {
    id: moreMeasure
    visible: false
    textFormat: Text.PlainText
    text: "+" + root.agents.length
    font.family: Style.font.family
    font.pixelSize: Style.font.caption
  }

  Row {
    id: strip
    spacing: Style.space(4)

    Text {
      id: chevron
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
        required property int index
        objectName: "agentChip"
        readonly property string agentText: Format.agentName(Model.agentId(modelData))
        readonly property string timeText: Format.relativeTime(Model.agentLastActivity(modelData), root.nowMs)
        anchors.verticalCenter: parent.verticalCenter
        radius: height / 2
        color: Util.alpha(Color.accent, 0.18)
        width: chipRow.implicitWidth + Style.spacing.rowPaddingX
        height: chipRow.implicitHeight + Style.space(2)
        visible: index < root.fitCount
        onWidthChanged: root.setChipWidth(index, width)
        Component.onCompleted: root.setChipWidth(index, width)

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
      visible: root.foldedCount > 0
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
        text: "+" + root.foldedCount
        font.family: Style.font.family
        font.pixelSize: Style.font.caption
        color: Color.foreground
      }
    }
  }
}
