import QtQuick
import qs.Ui
import qs.Commons
import qs.Commons as Commons
import "js/model.js" as Model
import "js/derive.js" as Derive
import "js/format.js" as Format

// The HERE card: the project the user is in (the focused terminal's, or
// the most recent), in the accent colour, with the facts line, the agent
// chips, the Next line and the three actions.
Rectangle {
  id: root
  objectName: "hereCard"

  // The card's width at the bar popup's size.
  property real restingWidth: width
  property var summary: null
  property string hereName: ""
  property string hereSource: ""
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
  readonly property string handoffBranch: root.summary ? Model.summaryHandoffBranch(root.summary) : ""
  readonly property string next: root.summary ? (Model.summaryHandoffNext(root.summary) || "") : ""
  readonly property var agents: root.summary ? Model.summaryAgents(root.summary) : []
  readonly property string originDevice: root.summary ? Model.summaryOriginDevice(root.summary) : ""
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
  color: Util.alpha(Commons.Color.accent, 0.18)
  border.width: 1
  border.color: Commons.Color.accent

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
        color: Commons.Color.popups.text
      }

      Text {
        anchors.verticalCenter: parent.verticalCenter
        textFormat: Text.PlainText
        text: "here"
        font.family: Style.font.family
        font.pixelSize: Style.font.caption
        font.bold: true
        font.letterSpacing: 1.2
        font.capitalization: Font.AllUppercase
        color: Commons.Color.accent
      }
    }

    Text {
      objectName: "hereSourceHint"
      visible: text !== ""
      width: parent.width
      textFormat: Text.PlainText
      text: Format.hereSourceHint(root.hereSource)
      font.family: Style.font.family
      font.pixelSize: Style.font.bodySmall
      color: Qt.darker(Commons.Color.popups.text, 1.3)
      elide: Text.ElideRight
    }

    Text {
      width: parent.width
      textFormat: Text.PlainText
      text: root.facts
      font.family: Style.font.family
      font.pixelSize: Style.font.bodySmall
      color: Qt.darker(Commons.Color.popups.text, 1.3)
      elide: Text.ElideRight
    }

    DeviceChip {
      objectName: "hereDeviceChip"
      device: root.originDevice
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
      text: Format.nextPrefix(root.handoffAgent, root.handoffBranch) + root.next
      font.family: Style.font.family
      font.pixelSize: Style.font.body
      color: Commons.Color.popups.text
      wrapMode: Text.WordWrap
    }

    // At the popup's size the buttons share one line and Continue takes the
    // width the other two leave, so the row ends at the card's inner edge.
    // A wider card keeps the row at that width against the right edge. A
    // narrower one narrows Continue down to its label, then breaks the
    // buttons onto more lines, each against the right edge. Labels keep
    // their size throughout.
    Item {
      id: buttons
      objectName: "hereButtons"
      readonly property real gap: Style.spacing.controlGap
      readonly property real restingWidth: root.restingWidth - (root.width - column.width)
      readonly property real othersWidth: terminalButton.width + gap + memoryButton.width
      readonly property bool oneLine: continueButton.implicitWidth + gap + othersWidth <= width
      readonly property bool othersOnOneLine: othersWidth <= width
      width: Math.min(parent.width, restingWidth)
      x: parent.width - width
      height: memoryButton.y + memoryButton.height

      LabelButton {
        id: continueButton
        objectName: "hereContinue"
        width: buttons.oneLine ? buttons.width - buttons.gap - buttons.othersWidth : Math.min(implicitWidth, buttons.width)
        x: buttons.oneLine ? 0 : buttons.width - width
        label: root.continueLabel
        onClicked: root.continueRequested()
      }
      LabelButton {
        id: terminalButton
        width: Math.min(implicitWidth, buttons.width)
        x: buttons.othersOnOneLine ? buttons.width - buttons.othersWidth : buttons.width - width
        y: buttons.oneLine ? 0 : continueButton.height + buttons.gap
        label: "Terminal"
        onClicked: root.terminalRequested()
      }
      LabelButton {
        id: memoryButton
        objectName: "hereMemory"
        width: Math.min(implicitWidth, buttons.width)
        x: buttons.width - width
        y: buttons.othersOnOneLine ? terminalButton.y : terminalButton.y + terminalButton.height + buttons.gap
        label: "Memory"
        onClicked: root.memoryRequested()
      }
    }
  }
}
