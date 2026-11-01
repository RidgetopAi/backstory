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
      width: root.width
      spacing: Style.spacing.controlGap
      leftPadding: Style.spacing.rowPaddingX
      rightPadding: Style.spacing.rowPaddingX

      Text {
        id: glyph
        textFormat: Text.PlainText
        text: Glyphs.attention()
        font.family: Style.font.family
        font.pixelSize: Style.font.bodySmall
        color: Commons.Color.urgent
      }

      Column {
        readonly property string reason: Model.attentionReason(itemRow.modelData)
        readonly property string detail: Format.attentionDetail(reason)
        id: texts
        width: itemRow.width - itemRow.leftPadding - itemRow.rightPadding - glyph.width - itemRow.spacing
          - (dismissButton.visible ? dismissButton.width + itemRow.spacing : 0)
        spacing: Style.space(2)

        Text {
          textFormat: Text.PlainText
          text: Format.attentionMain(texts.reason)
          font.family: Style.font.family
          font.pixelSize: Style.font.bodySmall
          color: Commons.Color.foreground
          width: parent.width
          wrapMode: Text.WordWrap
        }
        Row {
          visible: texts.detail !== ""
          spacing: Style.space(6)

          Text {
            textFormat: Text.PlainText
            text: Format.attentionDetailLabel(texts.detail)
            visible: text !== ""
            font.family: Style.font.family
            font.pixelSize: Style.font.caption
            color: Qt.darker(Commons.Color.foreground, 1.4)
          }
          Text {
            objectName: "attentionDetails"
            textFormat: Text.PlainText
            text: "Details"
            font.family: Style.font.family
            font.pixelSize: Style.font.caption
            font.underline: true
            color: detailsMouse.containsMouse ? Commons.Color.accent : Qt.darker(Commons.Color.foreground, 1.4)

            MouseArea {
              id: detailsMouse
              anchors.fill: parent
              hoverEnabled: true
            }
            PanelToolTip {
              visible: detailsMouse.containsMouse
              text: texts.detail
              delay: 0
              width: Math.min(implicitWidth, Style.space(300))
            }
          }
        }
      }

      LabelButton {
        id: dismissButton
        objectName: "attentionDismiss"
        visible: Model.attentionHandoffId(itemRow.modelData) !== ""
        label: "Dismiss"
        onClicked: root.dismiss(Model.attentionHandoffId(itemRow.modelData))
      }
    }
  }
}
