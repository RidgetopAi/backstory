import QtQuick
import Quickshell
import Quickshell.Io
import qs.Ui
import qs.Commons
import "js/launchers.js" as Launchers
import "js/groups.js" as Groups
import "js/glyphs.js" as Glyphs

// Gear editor: `backstory group set|clear|list` (PLAN.md Phase 4 "Human
// look": "Gear icon → group editor that calls `backstory group
// set|clear|list`"). This reads `backstory group list --json`, a
// different, already-documented CLI surface from this-week's own contract
// — its fields are intentionally NOT routed through js/model.js, which is
// scoped to the this-week contract panel_contract_test.go checks.
//
// Round 2 defect C (Brian's desk): grouping required TYPING a raw project
// key into a free-text field; the fields weren't visibly styled on the
// dark popup; setGroup() silently no-opped on an empty field; a failed
// `group set` was never shown. Redesign: every project (ungrouped or a
// group's member) is a clickable GroupProjectRow showing its display name
// — never a key the human types — and root.selectedProjectKey tracks the
// pick; the "+" is disabled until a project and a group name are both
// chosen (js/groups.js's canSubmitGroup, task 4fe02e30 DONE WHEN clause
// 5's "any pure JS the editor uses for selection/enablement is covered by
// a test"); a non-zero `group set|clear` exit renders its stderr below the
// fields instead of failing silently.
//
// Round 5 desk defect (Brian's desk, decision 9be5c1d5): this editor used
// to invent its own project name (js/groups.js's old projectDisplayName —
// text after the key's last "/"), so a git project_key rendered as its
// remote URL's basename ("omarcade.git") and a legacy/workspace key pair
// for the same folder rendered as two separate "projects" rows. Fixed
// store-side (`backstory group list --json` now also returns a `projects`
// array of {key, display_name, group}, display_name computed by the SAME
// function this-week's own rows use, and the legacy/workspace pair merged
// into one entry); this editor reads that array (js/groups.js's
// displayNameForKey) and never derives a name from a key itself.
//
// Round 6 desk defect A (Brian's desk, task 4fe02e30): this editor still
// built its ROWS from `ungrouped` and `groups[].projects` — the raw
// project_key lists, never deduped the way `projects` is — so a folder
// with history under both its legacy and workspace keys still showed up
// TWICE, the legacy one labelled with the raw key (displayNameForKey falls
// back to the key when it finds no match). Fixed: every Repeater below
// (a group's members, and the ungrouped section) is built ONLY from
// `groupsData.projects` (js/groups.js's projectsInGroup/ungroupedProjects),
// group membership read from each entry's own `group` field — `ungrouped`
// and `groups[].projects` are never read for rows, only `groups[].group`
// (the group NAMES) and the picker chips below.
Item {
  id: root

  signal changed()
  signal closeRequested()

  property var groupsData: ({ groups: [], ungrouped: [], projects: [] })
  property string selectedProjectKey: ""
  property string newGroupName: ""
  property string errorText: ""
  property bool busy: false

  function refresh() {
    listProcess.running = false
    listProcess.running = true
  }

  function pickProject(projectKey) {
    root.selectedProjectKey = (root.selectedProjectKey === projectKey) ? "" : projectKey
  }

  function setGroup(groupName, projectKey) {
    if (!Groups.canSubmitGroup(projectKey, groupName)) return
    root.errorText = ""
    mutateProcess.command = Launchers.groupSetCommand(groupName.trim(), projectKey)
    root.busy = true
    mutateProcess.running = true
  }

  function clearGroup(projectKey) {
    root.errorText = ""
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
          root.groupsData = { groups: [], ungrouped: [], projects: [] }
        }
      }
    }
  }

  Process {
    id: mutateProcess
    stderr: StdioCollector { id: mutateStderr }
    onExited: (exitCode, exitStatus) => {
      root.busy = false
      if (exitCode === 0) {
        root.errorText = ""
        root.selectedProjectKey = ""
        root.newGroupName = ""
        groupField.text = ""
        root.refresh()
        root.changed()
      } else {
        root.errorText = mutateStderr.text.length > 0
          ? mutateStderr.text
          : ("backstory group exited " + exitCode)
      }
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
        iconText: Glyphs.back()
        tooltipText: "Back"
        foreground: Color.foreground
        hoverColor: Color.accent
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
          model: Groups.projectsInGroup(root.groupsData.projects || [], modelData.group)
          delegate: GroupProjectRow {
            required property var modelData
            width: content.width
            projectKey: modelData.key
            displayName: modelData.display_name
            selected: root.selectedProjectKey === modelData.key
            removable: true
            indent: Style.space(12)
            onPicked: root.pickProject(modelData.key)
            onRemoveRequested: root.clearGroup(modelData.key)
          }
        }
      }
    }

    Text {
      visible: Groups.ungroupedProjects(root.groupsData.projects || []).length > 0
      textFormat: Text.PlainText
      text: "Ungrouped this week"
      font.family: Style.font.family
      font.pixelSize: Style.font.caption
      font.bold: true
      color: Qt.darker(Color.foreground, 1.3)
    }

    Repeater {
      model: Groups.ungroupedProjects(root.groupsData.projects || [])
      delegate: GroupProjectRow {
        required property var modelData
        width: content.width
        projectKey: modelData.key
        displayName: modelData.display_name
        selected: root.selectedProjectKey === modelData.key
        removable: false
        onPicked: root.pickProject(modelData.key)
      }
    }

    Flow {
      width: parent.width
      spacing: Style.spacing.controlGap
      visible: (root.groupsData.groups || []).length > 0

      Repeater {
        model: root.groupsData.groups || []
        delegate: Rectangle {
          id: chip
          required property var modelData
          radius: Style.cornerRadius
          border.width: 1
          border.color: Color.popups.border
          color: root.newGroupName === modelData.group ? Util.alpha(Color.accent, 0.3) : "transparent"
          implicitWidth: chipLabel.implicitWidth + Style.space(16)
          implicitHeight: chipLabel.implicitHeight + Style.space(8)

          Text {
            id: chipLabel
            anchors.centerIn: parent
            textFormat: Text.PlainText
            text: chip.modelData.group
            font.family: Style.font.family
            font.pixelSize: Style.font.caption
            color: Color.foreground
          }

          MouseArea {
            anchors.fill: parent
            cursorShape: Qt.PointingHandCursor
            onClicked: {
              root.newGroupName = chip.modelData.group
              groupField.text = chip.modelData.group
            }
          }
        }
      }
    }

    Text {
      visible: root.selectedProjectKey !== ""
      textFormat: Text.PlainText
      text: "Selected: " + Groups.displayNameForKey(root.groupsData.projects || [], root.selectedProjectKey)
      font.family: Style.font.family
      font.pixelSize: Style.font.caption
      color: Qt.darker(Color.foreground, 1.2)
    }

    Row {
      width: parent.width
      spacing: Style.spacing.controlGap

      TextField {
        id: groupField
        width: Style.spacing.dropdownWidth
        placeholderText: "group name"
        color: Color.foreground
        placeholderTextColor: Qt.darker(Color.foreground, 1.4)
        selectionColor: Color.accent
        background: Rectangle {
          radius: Style.cornerRadius
          color: Color.popups.background
          border.width: 1
          border.color: Color.popups.border
        }
        onTextChanged: root.newGroupName = text
      }

      PanelActionButton {
        iconText: Glyphs.addProject()
        tooltipText: root.selectedProjectKey === "" ? "Pick a project above first" : "Set group"
        foreground: Color.foreground
        hoverColor: Color.accent
        enabled: Groups.canSubmitGroup(root.selectedProjectKey, root.newGroupName) && !root.busy
        onClicked: root.setGroup(root.newGroupName, root.selectedProjectKey)
      }
    }

    Text {
      visible: root.errorText !== ""
      textFormat: Text.PlainText
      text: root.errorText
      wrapMode: Text.WordWrap
      width: parent.width
      font.family: Style.font.family
      font.pixelSize: Style.font.caption
      color: Color.urgent
    }
  }
}
