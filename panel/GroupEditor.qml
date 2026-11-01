import QtQuick
import Quickshell
import Quickshell.Io
import qs.Ui
import qs.Commons
import qs.Commons as Commons
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
  // Where the view is shown: the bar popup sets canPopOut, the window
  // canPin and pinned; the header then offers those buttons (HostButtons).
  property bool canPopOut: false
  property bool canPin: false
  property bool pinned: false
  signal popOutRequested()
  signal pinRequested()
  // The scrolling part under the header (ScrollPage), for the host and tests.
  readonly property alias flickable: page.flickable
  readonly property bool hasGroups: (root.groupsData.groups || []).length > 0
  readonly property bool hasUngrouped: Groups.ungroupedProjects(root.groupsData.projects || []).length > 0

  property var groupsData: ({ groups: [], ungrouped: [], projects: [] })
  property string selectedProjectKey: ""
  // The one highlighted project row's key, which the keys act on and hover
  // moves; null before there is one (a project's key can be "").
  property var cursorKey: null
  // The host's gate: hover moves the highlight only on a real pointer move.
  property PointerMoveGate pointer: null

  function hoverRow(key, item, mouse) {
    if (root.pointer.moved(item, mouse)) root.cursorKey = key
  }

  // Every row's project key, top to bottom: each group's members, then the
  // ungrouped projects.
  readonly property var rowKeys: {
    var all = root.groupsData.projects || []
    var keys = []
    var groups = root.groupsData.groups || []
    for (var i = 0; i < groups.length; i++)
      Groups.projectsInGroup(all, groups[i].group).forEach(function (p) { keys.push(p.key) })
    Groups.ungroupedProjects(all).forEach(function (p) { keys.push(p.key) })
    return keys
  }
  onRowKeysChanged: if (root.rowKeys.indexOf(root.cursorKey) < 0) root.cursorKey = root.rowKeys.length > 0 ? root.rowKeys[0] : null
  property string newGroupName: ""
  // True while the group-name field has the keys; the host takes them back
  // when it leaves Groups.
  readonly property bool typing: groupField.activeFocus
  property string errorText: ""
  property bool busy: false

  function refresh() {
    listProcess.running = false
    listProcess.running = true
  }

  // Set while Groups is the view showing. Each opening fetches the list
  // afresh (a project grouped in a terminal shows up) and starts with no
  // pick, name or error from the last.
  property bool shown: false
  onShownChanged: {
    if (!root.shown) return
    root.selectedProjectKey = ""
    root.newGroupName = ""
    groupField.text = ""
    root.errorText = ""
    root.refresh()
  }

  // "" is no pick, so a project whose key is "" never shows as picked.
  function isPicked(projectKey) {
    return root.selectedProjectKey !== "" && root.selectedProjectKey === projectKey
  }

  function pickProject(projectKey) {
    root.selectedProjectKey = (root.selectedProjectKey === projectKey) ? "" : projectKey
  }

  // j/k and the arrows move the highlight, as on the main view: up from the
  // first row shows the header again. Enter or Space picks the highlighted
  // project, as a click does. Returns whether it used the key.
  function handleKey(key) {
    switch (key) {
    case Qt.Key_J: case Qt.Key_Down: root.moveCursor(1); return true
    case Qt.Key_K: case Qt.Key_Up: root.moveCursor(-1); return true
    case Qt.Key_Return: case Qt.Key_Enter: case Qt.Key_Space:
      if (root.cursorKey !== null) root.pickProject(root.cursorKey)
      return true
    }
    return false
  }

  function moveCursor(delta) {
    var i = root.rowKeys.indexOf(root.cursorKey)
    if (i < 0) return
    if (delta < 0 && i === 0) { page.scrollToTop(); return }
    root.cursorKey = root.rowKeys[Math.max(0, Math.min(root.rowKeys.length - 1, i + delta))]
    var row = root.rowFor(root.cursorKey, page)
    if (row) page.reveal(row)
  }

  function rowFor(projectKey, item) {
    for (var i = 0; i < item.children.length; i++) {
      var child = item.children[i]
      if (child.projectKey === projectKey) return child
      var found = root.rowFor(projectKey, child)
      if (found) return found
    }
    return null
  }

  // The + is enabled, and Enter in the group name adds the group, once a
  // project is picked and a name typed.
  readonly property bool canSubmit: Groups.canSubmitGroup(root.selectedProjectKey, root.newGroupName) && !root.busy

  function submit() {
    if (root.canSubmit) root.setGroup(root.newGroupName, root.selectedProjectKey)
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

  implicitWidth: Style.space(360)
  implicitHeight: page.implicitHeight + Style.spacing.panelPadding * 2

  Rectangle {
    anchors.fill: parent
    radius: Style.cornerRadius
    color: Commons.Color.popups.background
  }

  // The header and the rule under it stay put; the groups, the ungrouped
  // projects and the form scroll. The rule under the header is the first
  // group's (or the ungrouped list's, or the form's), so each later section
  // draws its own.
  ScrollPage {
    id: page
    anchors.fill: parent
    anchors.margins: Style.spacing.panelPadding
    spacing: Style.spacing.rowGap

    header: [HeroHeader {
      id: header
      readonly property int groupCount: (root.groupsData.groups || []).length
      readonly property int projectCount: (root.groupsData.projects || []).length
      showBack: true
      title: "Groups"
      onBack: root.closeRequested()
      status: header.groupCount + (header.groupCount === 1 ? " group · " : " groups · ")
        + header.projectCount + (header.projectCount === 1 ? " project this week" : " projects this week")

      HostButtons {
        canPopOut: root.canPopOut
        canPin: root.canPin
        pinned: root.pinned
        onPopOutRequested: root.popOutRequested()
        onPinRequested: root.pinRequested()
      }
    },

    PanelSeparator { width: parent.width; foreground: Commons.Color.popups.text }]

    Repeater {
      model: root.groupsData.groups || []

      delegate: Column {
        required property var modelData
        required property int index
        width: page.width
        spacing: Style.spacing.labelGap / 2

        PanelSeparator { visible: index > 0; width: parent.width; foreground: Commons.Color.popups.text }

        Text {
          textFormat: Text.PlainText
          text: modelData.group
          font.family: Style.font.family
          font.pixelSize: Style.font.body
          font.bold: true
          color: Commons.Color.foreground
        }

        Repeater {
          model: Groups.projectsInGroup(root.groupsData.projects || [], modelData.group)
          delegate: GroupProjectRow {
            required property var modelData
            width: page.width
            projectKey: modelData.key
            displayName: modelData.display_name
            selected: root.isPicked(modelData.key)
            current: root.cursorKey === modelData.key
            removable: true
            indent: Style.space(12)
            onHovered: (item, mouse) => root.hoverRow(modelData.key, item, mouse)
            onPicked: root.pickProject(modelData.key)
            onRemoveRequested: root.clearGroup(modelData.key)
          }
        }
      }
    }

    PanelSeparator {
      width: parent.width
      visible: root.hasGroups && root.hasUngrouped
      foreground: Commons.Color.popups.text
    }

    Text {
      visible: Groups.ungroupedProjects(root.groupsData.projects || []).length > 0
      textFormat: Text.PlainText
      text: "Ungrouped this week"
      font.family: Style.font.family
      font.pixelSize: Style.font.caption
      font.bold: true
      color: Qt.darker(Commons.Color.foreground, 1.3)
    }

    Repeater {
      model: Groups.ungroupedProjects(root.groupsData.projects || [])
      delegate: GroupProjectRow {
        required property var modelData
        width: page.width
        projectKey: modelData.key
        displayName: modelData.display_name
        selected: root.isPicked(modelData.key)
        current: root.cursorKey === modelData.key
        removable: false
        onHovered: (item, mouse) => root.hoverRow(modelData.key, item, mouse)
        onPicked: root.pickProject(modelData.key)
      }
    }

    PanelSeparator {
      width: parent.width
      visible: root.hasGroups || root.hasUngrouped
      foreground: Commons.Color.popups.text
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
          border.color: Commons.Color.popups.border
          color: root.newGroupName === modelData.group ? Util.alpha(Commons.Color.accent, 0.3) : "transparent"
          implicitWidth: chipLabel.implicitWidth + Style.space(16)
          implicitHeight: chipLabel.implicitHeight + Style.space(8)

          Text {
            id: chipLabel
            anchors.centerIn: parent
            textFormat: Text.PlainText
            text: chip.modelData.group
            font.family: Style.font.family
            font.pixelSize: Style.font.caption
            color: Commons.Color.foreground
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
      color: Qt.darker(Commons.Color.foreground, 1.2)
    }

    Row {
      width: parent.width
      spacing: Style.spacing.controlGap

      TextField {
        id: groupField
        width: Style.spacing.dropdownWidth
        placeholderText: "group name"
        color: Commons.Color.foreground
        placeholderTextColor: Qt.darker(Commons.Color.foreground, 1.4)
        selectionColor: Commons.Color.accent
        background: Rectangle {
          radius: Style.cornerRadius
          color: Commons.Color.popups.background
          border.width: 1
          border.color: Commons.Color.popups.border
        }
        onTextChanged: root.newGroupName = text
        // Enter here is the form's, never the rows' pick: the field keeps
        // it rather than let it reach the view's keys.
        Keys.onReturnPressed: (event) => { root.submit(); event.accepted = true }
        Keys.onEnterPressed: (event) => { root.submit(); event.accepted = true }
      }

      PanelActionButton {
        iconText: Glyphs.addProject()
        tooltipText: root.selectedProjectKey === "" ? "Pick a project above first" : "Set group"
        foreground: Commons.Color.foreground
        hoverColor: Commons.Color.accent
        enabled: root.canSubmit
        onClicked: root.submit()
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
      color: Commons.Color.urgent
    }
  }
}
