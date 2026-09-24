import QtQuick
import qs.Ui
import qs.Commons
import "js/model.js" as Model

// The week: one small bar per (day, project) entry, most recent day first
// (PANEL-CONTRACT.md "The week"). Bar length is the entry's activity total
// (sessions + files touched + records written) relative to the busiest
// entry in the window — a relative-magnitude glance, not a precise chart.
Column {
  id: root

  property var days: []
  width: parent ? parent.width : Style.space(360)
  spacing: Style.spacing.labelGap / 2

  function activityOf(d) {
    return Model.daySessions(d) + Model.dayFilesTouched(d) + Model.dayRecordsWritten(d)
  }

  readonly property real maxActivity: {
    var max = 0
    for (var i = 0; i < root.days.length; i++) {
      var a = root.activityOf(root.days[i])
      if (a > max) max = a
    }
    return max
  }

  readonly property real barMaxWidth: Style.space(120)

  Repeater {
    model: root.days

    delegate: Row {
      id: dayRow
      required property var modelData
      width: root.width
      spacing: Style.spacing.controlGap
      leftPadding: Style.spacing.rowPaddingX
      rightPadding: Style.spacing.rowPaddingX

      Text {
        textFormat: Text.PlainText
        text: Model.dayDay(dayRow.modelData)
        font.family: Style.font.family
        font.pixelSize: Style.font.caption
        color: Qt.darker(Color.foreground, 1.3)
        width: Style.space(64)
      }

      Text {
        textFormat: Text.PlainText
        text: Model.dayDisplayName(dayRow.modelData)
        font.family: Style.font.family
        font.pixelSize: Style.font.bodySmall
        color: Color.foreground
        width: Style.space(120)
        elide: Text.ElideRight
      }

      Rectangle {
        anchors.verticalCenter: parent.verticalCenter
        height: Style.space(6)
        radius: height / 2
        color: Color.accent
        width: root.maxActivity > 0
          ? Math.max(Style.space(4), root.barMaxWidth * root.activityOf(dayRow.modelData) / root.maxActivity)
          : Style.space(4)
      }
    }
  }

  Text {
    visible: root.days.length === 0
    textFormat: Text.PlainText
    text: "No activity recorded this week."
    font.family: Style.font.family
    font.pixelSize: Style.font.bodySmall
    color: Qt.darker(Color.foreground, 1.3)
    leftPadding: Style.spacing.rowPaddingX
  }
}
