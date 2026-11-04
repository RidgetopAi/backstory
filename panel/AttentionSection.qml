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

  // Dismiss on a stale-handoff row: the human's own "this is still true",
  // run through the CLI (the only route to a human-declared affirm).
  function dismiss(handoffId) {
    if (!handoffId) return
    affirmProcess.command = Launchers.affirmCommand(handoffId)
    affirmProcess.running = true
  }

  Process {
    id: affirmProcess
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
      readonly property string stillRightTip: "Mark this handoff as still correct; it stops showing here until something new happens."
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
          - (dismissButton.visible ? dismissButton.width + itemRow.spacing : 0)
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
            ? Format.staleHeadline(Format.attentionLabel(texts.reason), Model.attentionHandoffTime(itemRow.modelData))
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
          text: Format.sinceThen(itemRow.reasons)
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

      LabelButton {
        id: dismissButton
        objectName: "attentionDismiss"
        visible: itemRow.handoffId !== ""
        label: "Still right"
        onClicked: root.dismiss(itemRow.handoffId)

        HoverHandler { id: stillRightHover }
        PanelToolTip {
          objectName: "attentionStillRightTip"
          visible: stillRightHover.hovered
          text: itemRow.stillRightTip
          delay: 0
          width: Math.min(implicitWidth, Style.space(300))
        }
      }
    }
  }
}
