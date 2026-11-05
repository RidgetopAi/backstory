import QtQuick
import Quickshell.Io
import qs.Ui
import qs.Commons
import qs.Commons as Commons
import "js/model.js" as Model
import "js/glyphs.js" as Glyphs
import "js/launchers.js" as Launchers
import "js/format.js" as Format

// Attention: positive evidence only, rendered only when non-empty — an
// empty attention array is itself "nothing to flag this week", not the
// absence of a key to guard against (PANEL-CONTRACT.md "Attention",
// decision 9be5c1d5). The caller (Panel.qml) is responsible for the
// `visible: items.length > 0` gate; this component just renders rows.
Column {
  id: root

  property var items: []

  // Emitted once `backstory affirm <handoff-id>` has run, so the host can
  // refresh and the dismissed item disappears.
  signal dismissed()

  // Update on a stale-handoff row: the host opens an agent in the project's
  // folder with this opening prompt.
  signal updateRequested(string projectKey, string handoffId, string prompt)

  // Dismiss on a stale-handoff row: the human's own "this is still true",
  // run through the CLI (the only route to a human-declared affirm).
  function dismiss(handoffId) {
    if (!handoffId) return
    affirmProcess.command = Launchers.affirmCommand(handoffId)
    affirmProcess.running = true
  }

  // Done on a stale-handoff row: the CLI marks the handoff finished.
  function markDone(handoffId) {
    if (!handoffId) return
    doneProcess.command = Launchers.doneCommand(handoffId)
    doneProcess.running = true
  }

  Process {
    id: affirmProcess
    onExited: (exitCode, exitStatus) => root.dismissed()
  }

  Process {
    id: doneProcess
    onExited: (exitCode, exitStatus) => root.dismissed()
  }
  width: parent ? parent.width : Style.space(360)
  spacing: Style.spacing.labelGap

  Repeater {
    model: root.items

    delegate: Row {
      id: itemRow
      required property var modelData
      readonly property string handoffId: Model.attentionHandoffId(modelData)
      readonly property var reasons: Model.attentionReasons(modelData)
      // A stale-handoff row from a payload that carries the structured
      // fields shows them; any other row shows its reason as it always did.
      readonly property bool structured: handoffId !== "" && Model.attentionHandoffTime(modelData) !== ""
      readonly property bool looksFinished: Model.attentionLooksFinished(modelData)
      readonly property string stillRightTip: "Mark this handoff as still correct; it stops showing here until something new happens."
      readonly property string updateTip: "Open an agent here to bring this handoff up to date."
      readonly property string doneTip: "This work is finished. Stop offering it to resume; nothing is deleted."
      // Done leads when an outcome closed the work, else Still right does.
      readonly property var actionOrder: looksFinished ? ["done", "dismiss", "update"] : ["dismiss", "update", "done"]
      property bool expanded: false
      width: root.width
      spacing: Style.spacing.controlGap
      leftPadding: Style.spacing.rowPaddingX
      rightPadding: Style.spacing.rowPaddingX
      activeFocusOnTab: structured
      objectName: "attentionRow"

      function toggle() { if (itemRow.structured) itemRow.expanded = !itemRow.expanded }

      Keys.onPressed: (event) => {
        if (itemRow.structured && (event.key === Qt.Key_Return || event.key === Qt.Key_Enter || event.key === Qt.Key_Space)) {
          itemRow.toggle()
          event.accepted = true
        }
      }

      Text {
        id: glyph
        textFormat: Text.PlainText
        text: Glyphs.attention()
        font.family: Style.font.family
        font.pixelSize: Style.font.bodySmall
        color: Commons.Color.urgent
      }

      Item {
        id: textBox
        width: itemRow.width - itemRow.leftPadding - itemRow.rightPadding - glyph.width - itemRow.spacing
          - (actions.visible ? actions.width + itemRow.spacing : 0)
        height: texts.height

        // A click on the text (the button is a sibling) toggles the row and
        // takes focus, so Enter and Space then do the same.
        MouseArea {
          objectName: "attentionRowClick"
          anchors.fill: parent
          enabled: itemRow.structured
          onClicked: { itemRow.forceActiveFocus(); itemRow.toggle() }
        }

      Column {
        readonly property string reason: Model.attentionReason(itemRow.modelData)
        id: texts
        width: parent.width
        spacing: Style.space(2)

        Text {
          objectName: "attentionHeadline"
          textFormat: Text.PlainText
          text: itemRow.structured
            ? Format.staleHeadline(Format.attentionLabel(texts.reason), Model.attentionHandoffTime(itemRow.modelData), itemRow.looksFinished)
            : Format.attentionMain(texts.reason)
          font.family: Style.font.family
          font.pixelSize: Style.font.bodySmall
          color: Commons.Color.foreground
          width: parent.width
          wrapMode: Text.WordWrap
        }
        Text {
          objectName: "attentionSummary"
          visible: itemRow.structured && text !== ""
          textFormat: Text.PlainText
          text: Model.attentionHandoffSummary(itemRow.modelData)
          font.family: Style.font.family
          font.pixelSize: Style.font.caption
          color: Qt.darker(Commons.Color.foreground, 1.4)
          width: parent.width
          wrapMode: Text.WordWrap
        }
        Text {
          objectName: "attentionSince"
          visible: itemRow.structured && text !== ""
          textFormat: Text.PlainText
          text: Format.sinceThen(itemRow.reasons, itemRow.looksFinished)
          font.family: Style.font.family
          font.pixelSize: Style.font.caption
          color: Qt.darker(Commons.Color.foreground, 1.4)
          width: parent.width
          wrapMode: Text.WordWrap
          maximumLineCount: itemRow.expanded ? 1000 : 2
          elide: itemRow.expanded ? Text.ElideNone : Text.ElideRight
        }
        Column {
          objectName: "attentionReasons"
          visible: itemRow.structured && itemRow.expanded
          width: parent.width
          spacing: Style.space(4)

          Repeater {
            model: itemRow.expanded ? itemRow.reasons : []

            delegate: Column {
              required property var modelData
              width: parent.width
              spacing: Style.space(1)

              Text {
                objectName: "attentionReasonWhen"
                visible: text !== ""
                textFormat: Text.PlainText
                text: Format.reasonWhenWhere(parent.modelData)
                font.family: Style.font.family
                font.pixelSize: Style.font.caption
                color: Qt.darker(Commons.Color.foreground, 1.4)
                width: parent.width
                wrapMode: Text.WrapAnywhere
              }
              Text {
                objectName: "attentionReasonText"
                textFormat: Text.PlainText
                text: parent.modelData.text
                font.family: Style.font.family
                font.pixelSize: Style.font.caption
                color: Commons.Color.foreground
                width: parent.width
                wrapMode: Text.WrapAnywhere
              }
            }
          }
        }
        Text {
          objectName: "attentionToggle"
          visible: itemRow.structured && itemRow.reasons.length > 0
          textFormat: Text.PlainText
          text: itemRow.expanded ? "Hide" : "Show"
          font.family: Style.font.family
          font.pixelSize: Style.font.caption
          font.underline: true
          color: Qt.darker(Commons.Color.foreground, 1.4)
        }
      }
      }

      // The actions stack beside the text so the column keeps its width at
      // the bar popup's size; every button takes the widest label's width.
      Column {
        id: actions
        objectName: "attentionActions"
        visible: itemRow.handoffId !== ""
        spacing: Style.spacing.labelGap

        Repeater {
          model: itemRow.actionOrder

          delegate: LabelButton {
            id: actionButton
            required property string modelData
            required property int index
            readonly property bool isDone: modelData === "done"
            readonly property bool isUpdate: modelData === "update"
            objectName: isDone ? "attentionDone" : (isUpdate ? "attentionUpdate" : "attentionDismiss")
            label: isDone ? "Done" : (isUpdate ? "Update" : "Still right")
            primary: index === 0 && itemRow.looksFinished
            width: Math.max(implicitWidth, actionsMetrics.needed)
            activeFocusOnTab: true
            onClicked: {
              if (isDone) root.markDone(itemRow.handoffId)
              else if (isUpdate) root.updateRequested(Model.attentionProjectKey(itemRow.modelData),
                itemRow.handoffId, Format.updatePrompt(itemRow.handoffId, itemRow.looksFinished, itemRow.reasons))
              else root.dismiss(itemRow.handoffId)
            }

            HoverHandler { id: actionHover }
            PanelToolTip {
              objectName: actionButton.isDone ? "attentionDoneTip" : (actionButton.isUpdate ? "attentionUpdateTip" : "attentionStillRightTip")
              visible: actionHover.hovered
              text: actionButton.isDone ? itemRow.doneTip : (actionButton.isUpdate ? itemRow.updateTip : itemRow.stillRightTip)
              delay: 0
              width: Math.min(implicitWidth, Style.space(300))
            }
          }
        }
      }

      // Widest of the three labels, so the stack is one width.
      TextMetrics {
        id: actionsMetrics
        font.family: Style.font.family
        font.pixelSize: Style.font.bodySmall
        font.bold: true
        text: "Still right"
        readonly property real needed: advanceWidth + Style.spacing.rowPaddingX * 2
      }
    }
  }
}
