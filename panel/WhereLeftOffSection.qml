import QtQuick
import qs.Ui
import qs.Commons
import "js/model.js" as Model
import "js/glyphs.js" as Glyphs

// Recent: one row per project, most recently active first, with
// a project in a user group collapsed into one expandable row with its
// group-mates as children (PANEL-CONTRACT.md "Where you left off").
Column {
  id: root

  property var rows: []

  signal openTerminal(string cwd)
  signal continueRequested(var summary)
  signal openMemory(string projectKey, string displayName, string cwd)

  // Which group rows are expanded, keyed by group name. Collapsed by
  // default — a group is a human-made summary already; a click reveals it,
  // not the other way around.
  property var expandedGroups: ({})
  property var week: []
  // Identity (key + cwd) of the keyboard-selected row, "" for none.
  property string selectedId: ""

  signal toggleGroup(string group)

  // Which folder rows show their per-agent sub-rows, keyed by row key plus
  // cwd (Panel.rowId); selectedAgent is the keyboard-selected sub-row's agent.
  property var expandedRows: ({})
  property string selectedAgent: ""
  property real nowMs: Date.now()

  signal toggleRow(var summary)
  signal launchRequested(var summary, string agent)

  function rowKey(summary) {
    return Model.summaryProjectKey(summary) + "\n" + Model.summaryCwd(summary)
  }

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
            root.toggleGroup(Model.rowGroup(rowDelegate.modelData))
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
          delegate: Column {
            id: childDelegate
            required property var modelData
            readonly property string key: root.rowKey(modelData)
            width: root.width
            spacing: 0

            ProjectRow {
              width: root.width
              summary: childDelegate.modelData
              indented: true
              onOpenTerminal: (cwd) => root.openTerminal(cwd)
              week: root.week
              nowMs: root.nowMs
              expanded: !!root.expandedRows[childDelegate.key]
              selected: root.selectedId === childDelegate.key && root.selectedAgent === ""
              onContinueRequested: root.continueRequested(childDelegate.modelData)
              onToggleExpanded: root.toggleRow(childDelegate.modelData)
              onOpenMemory: (projectKey, displayName, cwd) => root.openMemory(projectKey, displayName, cwd)
            }

            AgentSubRows {
              visible: !!root.expandedRows[childDelegate.key]
              width: root.width
              summary: childDelegate.modelData
              nowMs: root.nowMs
              selectedAgent: root.selectedId === childDelegate.key ? root.selectedAgent : ""
              onLaunchRequested: (agent) => root.launchRequested(childDelegate.modelData, agent)
            }
          }
        }
      }

      // Standalone row.
      ProjectRow {
        visible: !rowDelegate.isGroup
        width: root.width
        summary: rowDelegate.isGroup ? null : Model.rowProject(rowDelegate.modelData)
        onOpenTerminal: (cwd) => root.openTerminal(cwd)
        week: root.week
        nowMs: root.nowMs
        expanded: !rowDelegate.isGroup && !!root.expandedRows[root.rowKey(summary)]
        selected: !rowDelegate.isGroup && root.selectedId === root.rowKey(summary) && root.selectedAgent === ""
        onContinueRequested: root.continueRequested(summary)
        onToggleExpanded: root.toggleRow(summary)
        onOpenMemory: (projectKey, displayName, cwd) => root.openMemory(projectKey, displayName, cwd)
      }

      AgentSubRows {
        visible: !rowDelegate.isGroup && !!root.expandedRows[root.rowKey(summary)]
        width: root.width
        summary: rowDelegate.isGroup ? null : Model.rowProject(rowDelegate.modelData)
        nowMs: root.nowMs
        selectedAgent: !rowDelegate.isGroup && root.selectedId === root.rowKey(summary) ? root.selectedAgent : ""
        onLaunchRequested: (agent) => root.launchRequested(summary, agent)
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
