import QtQuick
import Quickshell
import Quickshell.Io
import qs.Ui
import qs.Commons
import "js/launchers.js" as Launchers
import "js/glyphs.js" as Glyphs

// Gear editor: `backstory group set|clear|list` (PLAN.md Phase 4 "Human
// look": "Gear icon → group editor that calls `backstory group
// set|clear|list`"). This reads `backstory group list --json`, a
// different, already-documented CLI surface from this-week's own contract
// — its fields are intentionally NOT routed through js/model.js, which is
// scoped to the this-week contract panel_contract_test.go checks.
Item {
  id: root

  signal changed()
  signal closeRequested()

  property var groupsData: ({ groups: [], ungrouped: [] })
  property string newGroupName: ""
  property string newProjectKey: ""
  property bool busy: false

  function refresh() {
    listProcess.running = false
    listProcess.running = true
  }

  function setGroup(groupName, projectKey) {
    if (groupName === "" || projectKey === "") return
    mutateProcess.command = Launchers.groupSetCommand(groupName, projectKey)
    root.busy = true
    mutateProcess.running = true
  }

  function clearGroup(projectKey) {
    mutateProcess.command = Launchers.groupClearCommand(projectKey)
    root.busy = true
    mutateProcess.running = true
  }

  Process {
    id: listProcess
    command: Launchers.groupListCommand()
    stdout: StdioCollector {
      onStreamFinished: {
        try {
          root.groupsData = JSON.parse(text || "{}")
        } catch (e) {
          root.groupsData = { groups: [], ungrouped: [] }
        }
      }
    }
  }

  Process {
    id: mutateProcess
    onExited: (exitCode, exitStatus) => {
      root.busy = false
      root.refresh()
      root.changed()
    }
  }

  Component.onCompleted: root.refresh()

  implicitWidth: Style.space(360)
  implicitHeight: content.implicitHeight + Style.spacing.panelPadding * 2

  Rectangle {
    anchors.fill: parent
    radius: Style.cornerRadius
    color: Color.popups.background
  }

  Column {
    id: content
    anchors.fill: parent
    anchors.margins: Style.spacing.panelPadding
    spacing: Style.spacing.rowGap

    Row {
      width: parent.width
      Text {
        textFormat: Text.PlainText
        text: "Groups"
        font.family: Style.font.family
        font.pixelSize: Style.font.title
        font.bold: true
        color: Color.foreground
        width: parent.width - Style.space(24)
      }
      PanelActionButton {
        iconText: Glyphs.close()
        tooltipText: "Close"
        foreground: Color.foreground
        hoverColor: Color.urgent
        onClicked: root.closeRequested()
      }
    }

    Repeater {
      model: root.groupsData.groups || []

      delegate: Column {
        required property var modelData
        width: content.width
        spacing: Style.spacing.labelGap / 2

        Text {
          textFormat: Text.PlainText
          text: modelData.group
          font.family: Style.font.family
          font.pixelSize: Style.font.body
          font.bold: true
          color: Color.foreground
        }

        Repeater {
          model: modelData.projects || []
          delegate: Row {
            required property string modelData
            spacing: Style.spacing.controlGap
            leftPadding: Style.space(12)

            Text {
              textFormat: Text.PlainText
              text: modelData
              font.family: Style.font.family
              font.pixelSize: Style.font.bodySmall
              color: Color.foreground
            }

            PanelActionButton {
              iconText: Glyphs.close()
              tooltipText: "Remove from group"
              foreground: Color.foreground
              hoverColor: Color.urgent
              size: Style.space(16)
              fontSize: Style.font.caption
              onClicked: root.clearGroup(modelData)
            }
          }
        }
      }
    }

    Text {
      visible: (root.groupsData.ungrouped || []).length > 0
      textFormat: Text.PlainText
      text: "Ungrouped this week"
      font.family: Style.font.family
      font.pixelSize: Style.font.caption
      font.bold: true
      color: Qt.darker(Color.foreground, 1.3)
    }

    Repeater {
      model: root.groupsData.ungrouped || []
      delegate: Text {
        required property string modelData
        textFormat: Text.PlainText
        text: modelData
        font.family: Style.font.family
        font.pixelSize: Style.font.bodySmall
        color: Color.foreground
        leftPadding: Style.space(12)
      }
    }

    Row {
      width: parent.width
      spacing: Style.spacing.controlGap

      TextField {
        id: groupField
        width: Style.spacing.dropdownWidth
        placeholderText: "group name"
        onTextChanged: root.newGroupName = text
      }

      TextField {
        id: projectField
        width: Style.spacing.dropdownWidth
        placeholderText: "project key"
        onTextChanged: root.newProjectKey = text
      }

      PanelActionButton {
        iconText: Glyphs.addProject()
        tooltipText: "Set group"
        foreground: Color.foreground
        hoverColor: Color.accent
        enabled: !root.busy
        onClicked: root.setGroup(root.newGroupName, root.newProjectKey)
      }
    }
  }
}
