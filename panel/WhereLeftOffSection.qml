import QtQuick
import qs.Ui
import qs.Commons
import "js/model.js" as Model
import "js/glyphs.js" as Glyphs

// Where you left off: one row per project, most recently active first, with
// a project in a user group collapsed into one expandable row with its
// group-mates as children (PANEL-CONTRACT.md "Where you left off").
Column {
  id: root

  property var rows: []

  signal openTerminal(string cwd)
  signal resumeAgent(string cwd, string handoffText, string handoffId)

  // Which group rows are expanded, keyed by group name. Collapsed by
  // default — a group is a human-made summary already; a click reveals it,
  // not the other way around.
  property var expandedGroups: ({})

  width: parent ? parent.width : Style.space(360)
  spacing: 0

  Repeater {
    model: root.rows

    delegate: Column {
      id: rowDelegate
      required property var modelData
      readonly property bool isGroup: Model.rowGroup(modelData) !== undefined && Model.rowGroup(modelData) !== ""
      width: root.width
      spacing: 0

      // Group row: a header ("<group> (N)") that expands to its children.
      Item {
        visible: rowDelegate.isGroup
        width: root.width
        implicitHeight: groupHeaderRow.implicitHeight + Style.spacing.rowGap

        MouseArea {
          anchors.fill: parent
          cursorShape: Qt.PointingHandCursor
          onClicked: {
            var g = Model.rowGroup(rowDelegate.modelData)
            var next = Object.assign({}, root.expandedGroups)
            next[g] = !next[g]
            root.expandedGroups = next
          }
        }

        Row {
          id: groupHeaderRow
          anchors.left: parent.left
          anchors.leftMargin: Style.spacing.rowPaddingX
          anchors.verticalCenter: parent.verticalCenter
          spacing: Style.spacing.controlGap

          Text {
            textFormat: Text.PlainText
            text: root.expandedGroups[Model.rowGroup(rowDelegate.modelData)] ? Glyphs.chevronDown() : Glyphs.chevronRight()
            font.family: Style.font.family
            font.pixelSize: Style.font.bodySmall
            color: Color.foreground
          }

          Text {
            textFormat: Text.PlainText
            text: Model.rowGroup(rowDelegate.modelData) + " (" + Model.rowChildren(rowDelegate.modelData).length + ")"
            font.family: Style.font.family
            font.pixelSize: Style.font.body
            font.bold: true
            color: Color.foreground
          }
        }
      }

      Column {
        visible: rowDelegate.isGroup && !!root.expandedGroups[Model.rowGroup(rowDelegate.modelData)]
        width: root.width
        spacing: 0

        Repeater {
          model: rowDelegate.isGroup ? Model.rowChildren(rowDelegate.modelData) : []
          delegate: ProjectRow {
            required property var modelData
            width: root.width
            summary: modelData
            indented: true
            onOpenTerminal: (cwd) => root.openTerminal(cwd)
            onResumeAgent: (cwd, handoffText, handoffId) => root.resumeAgent(cwd, handoffText, handoffId)
          }
        }
      }

      // Standalone row.
      ProjectRow {
        visible: !rowDelegate.isGroup
        width: root.width
        summary: rowDelegate.isGroup ? null : Model.rowProject(rowDelegate.modelData)
        onOpenTerminal: (cwd) => root.openTerminal(cwd)
        onResumeAgent: (cwd, handoffText, handoffId) => root.resumeAgent(cwd, handoffText, handoffId)
      }
    }
  }

  Text {
    visible: root.rows.length === 0
    textFormat: Text.PlainText
    text: "Nothing this week."
    font.family: Style.font.family
    font.pixelSize: Style.font.bodySmall
    color: Qt.darker(Color.foreground, 1.3)
    leftPadding: Style.spacing.rowPaddingX
  }
}
