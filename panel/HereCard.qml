import QtQuick
import qs.Ui
import qs.Commons
import "js/model.js" as Model
import "js/derive.js" as Derive
import "js/format.js" as Format

// The HERE card: the project the user is in (the focused terminal's, or
// the most recent), in the accent colour, with the facts line, the agent
// chips, the Next line and the three actions.
Rectangle {
  id: root

  property var summary: null
  property string hereName: ""
  property string defaultAgent: ""
  property string continueLabel: ""
  property var week: []
  property bool expanded: false
  property real nowMs: Date.now()

  signal continueRequested()
  signal toggleExpanded()
  signal terminalRequested()
  signal memoryRequested()

  readonly property string lastAgent: root.summary ? (Model.summaryLastAgent(root.summary) || "") : ""
  readonly property string next: root.summary ? (Model.summaryHandoffNext(root.summary) || "") : ""
  readonly property var agents: root.summary ? Model.summaryAgents(root.summary) : []
  readonly property string handoffAgent: root.summary ? Model.summaryHandoffAgent(root.summary) : ""
  readonly property string facts: {
    var parts = []
    if (root.lastAgent) parts.push(Format.agentName(root.lastAgent))
    var last = root.summary ? Model.summaryLastActivity(root.summary) : ""
    if (last) parts.push(Format.relativeTime(last, root.nowMs))
    parts.push(Derive.sessionsInWeek(root.week, root.summary ? Model.summaryProjectKey(root.summary) : "") + " sessions this week")
    return parts.join(" · ")
  }

  implicitHeight: column.implicitHeight + Style.spacing.rowGap * 2
  radius: Style.cornerRadius
  color: Util.alpha(Color.accent, 0.14)
  border.width: 1
  border.color: Color.accent

  Column {
    id: column
    anchors.left: parent.left
    anchors.right: parent.right
    anchors.top: parent.top
    anchors.margins: Style.spacing.rowGap
    spacing: Style.spacing.labelGap

    Row {
      spacing: Style.spacing.controlGap

      Text {
        objectName: "hereTitle"
        textFormat: Text.PlainText
        text: root.hereName
        font.family: Style.font.family
        font.pixelSize: Style.font.title
        font.bold: true
        color: Color.popups.text
      }

      Rectangle {
        anchors.verticalCenter: parent.verticalCenter
        radius: height / 2
        color: Color.accent
        width: chip.implicitWidth + Style.spacing.rowPaddingX * 2
        height: chip.implicitHeight + Style.space(2)

        Text {
          id: chip
          anchors.centerIn: parent
          textFormat: Text.PlainText
          text: "here"
          font.family: Style.font.family
          font.pixelSize: Style.font.caption
          color: Color.popups.background
        }
      }
    }

    Text {
      width: parent.width
      textFormat: Text.PlainText
      text: root.facts
      font.family: Style.font.family
      font.pixelSize: Style.font.bodySmall
      color: Qt.darker(Color.popups.text, 1.3)
      elide: Text.ElideRight
    }

    AgentChips {
      objectName: "hereAgentChips"
      agents: root.agents
      expanded: root.expanded
      nowMs: root.nowMs
      onToggled: root.toggleExpanded()
    }

    Text {
      visible: root.next !== ""
      width: parent.width
      textFormat: Text.PlainText
      text: Format.nextPrefix(root.handoffAgent) + root.next
      font.family: Style.font.family
      font.pixelSize: Style.font.body
      color: Color.popups.text
      wrapMode: Text.WordWrap
    }

    Row {
      spacing: Style.spacing.controlGap

      LabelButton { label: root.continueLabel; primary: true; onClicked: root.continueRequested() }
      LabelButton { label: "Terminal"; onClicked: root.terminalRequested() }
      LabelButton { label: "Memory"; onClicked: root.memoryRequested() }
    }
  }
}
