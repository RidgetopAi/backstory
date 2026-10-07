import QtQuick
import Quickshell.Io
import qs.Ui
import qs.Commons
import "js/model.js" as Model
import "js/glyphs.js" as Glyphs
import "js/launchers.js" as Launchers

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
        textFormat: Text.PlainText
        text: Glyphs.attention()
        font.family: Style.font.family
        font.pixelSize: Style.font.bodySmall
        color: Color.urgent
      }

      Text {
        textFormat: Text.PlainText
        text: Model.attentionReason(itemRow.modelData)
        font.family: Style.font.family
        font.pixelSize: Style.font.bodySmall
        color: Color.foreground
        width: itemRow.width - Style.space(24) - (dismissButton.visible ? dismissButton.width + itemRow.spacing : 0)
        wrapMode: Text.WordWrap
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
