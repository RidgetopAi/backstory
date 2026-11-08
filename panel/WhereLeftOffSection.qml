import QtQuick
import qs.Ui
import qs.Commons
import qs.Commons as Commons
import "js/model.js" as Model
import "js/glyphs.js" as Glyphs

// Recent: one row per project, most recently active first
// (PANEL-CONTRACT.md "Where you left off").
Column {
  id: root

  property var rows: []
  // True while the panel waits for its first data: no "Nothing this week."
  property bool waiting: false

  signal openTerminal(string cwd)
  signal continueRequested(var summary)
  signal openMemory(string projectKey, string displayName, string cwd)

  property var week: []
  // Identity (key + cwd) of the keyboard-selected row, "" for none.
  property string selectedId: ""

  // Which folder rows show their per-agent sub-rows, keyed by row key plus
  // cwd (Panel.rowId); selectedAgent is the keyboard-selected sub-row's agent.
  property var expandedRows: ({})
  property string selectedAgent: ""
  property real nowMs: Date.now()

  signal toggleRow(var summary)
  signal launchRequested(var summary, string agent)
  signal hoverRow(var summary, string agent, var item, var mouse)

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
      width: root.width
      spacing: 0

      ProjectRow {
        width: root.width
        summary: Model.rowProject(rowDelegate.modelData)
        onOpenTerminal: (cwd) => root.openTerminal(cwd)
        week: root.week
        nowMs: root.nowMs
        expanded: !!root.expandedRows[root.rowKey(summary)]
        selected: root.selectedId === root.rowKey(summary) && root.selectedAgent === ""
        onContinueRequested: root.continueRequested(summary)
        onToggleExpanded: root.toggleRow(summary)
        onOpenMemory: (projectKey, displayName, cwd) => root.openMemory(projectKey, displayName, cwd)
        onHovered: (item, mouse) => root.hoverRow(summary, "", item, mouse)
      }

      AgentSubRows {
        visible: !!root.expandedRows[root.rowKey(summary)]
        width: root.width
        summary: Model.rowProject(rowDelegate.modelData)
        nowMs: root.nowMs
        selectedAgent: root.selectedId === root.rowKey(summary) ? root.selectedAgent : ""
        onLaunchRequested: (agent) => root.launchRequested(summary, agent)
        onHoveredAgent: (agent, item, mouse) => root.hoverRow(summary, agent, item, mouse)
      }
    }
  }

  Text {
    visible: root.rows.length === 0 && !root.waiting
    textFormat: Text.PlainText
    text: "Nothing this week."
    font.family: Style.font.family
    font.pixelSize: Style.font.bodySmall
    color: Qt.darker(Commons.Color.foreground, 1.3)
    leftPadding: Style.spacing.rowPaddingX
  }
}
