import QtQuick
import QtTest
import Quickshell
import Quickshell.Io
import "testutil.js" as TestUtil
import "../../js/launchers.js" as Launchers

// The embedded GroupEditor suite (task 4fe02e30 round 4 desk defect B: the
// editor used to render OUTSIDE the PanelWindow's own surface — see
// Panel.qml's own doc comment on why it now lives inside the same
// Flickable as the normal sections). Drives the real gear -> pick a
// project -> type a group -> "+" click path and asserts the resulting
// `backstory group set <group> <key>` argv (DONE WHEN clause 4), and that
// a stubbed non-zero `group set` exit shows its stderr in the editor
// (DONE WHEN clause 1's harness spec, clause 4).
TestCase {
  id: testCase
  name: "GroupEditor"
  when: windowShown
  visible: true
  width: 900
  height: 900

  readonly property string ungroupedProjectKey: "/home/brian/acme-week-main"

  Component {
    id: panelComponent
    Loader { source: Qt.resolvedUrl("../../Panel.qml") }
  }

  property var loader: null
  property var panel: null

  function init() {
    ProcessController.reset()
    Quickshell.reset()
    ProcessController.respond(Launchers.thisWeekCommand(), {
      stdout: JSON.stringify({ attention: [], where_left_off: [], week: [] }), stderr: "", exitCode: 0
    })
    ProcessController.respond(Launchers.groupListCommand(), {
      stdout: JSON.stringify({ groups: [], ungrouped: [ungroupedProjectKey] }), stderr: "", exitCode: 0
    })

    loader = panelComponent.createObject(testCase)
    tryCompare(loader, "status", Loader.Ready, 2000)
    panel = loader.item
    panel.testContentItem.parent = loader
    panel.open("{}")
    wait(20)
  }

  function cleanup() {
    if (loader) loader.destroy()
    loader = null
    panel = null
  }

  function clickByTooltip(tooltipText) {
    var button = TestUtil.findFirst(panel.testContentItem, function (node) {
      return node.tooltipText === tooltipText
    })
    verify(button !== null, "no PanelActionButton with tooltipText " + JSON.stringify(tooltipText))
    mouseClick(button, button.width / 2, button.height / 2)
  }

  function openGroupEditor() {
    clickByTooltip("Groups")
    wait(20)
    verify(panel.groupEditorOpen)
  }

  function pickUngroupedProject() {
    var row = TestUtil.findFirst(panel.testContentItem, function (node) {
      return node.projectKey === ungroupedProjectKey
    })
    verify(row !== null, "no GroupProjectRow for " + ungroupedProjectKey)
    mouseClick(row, row.width / 2, row.height / 2)
    wait(10)
  }

  function typeGroupName(name) {
    var field = TestUtil.findFirst(panel.testContentItem, function (node) {
      return node.placeholderText === "group name"
    })
    verify(field !== null, "no group-name TextField")
    field.text = name
    wait(10)
  }

  function test_add_group_runs_backstory_group_set() {
    openGroupEditor()
    pickUngroupedProject()
    typeGroupName("work")

    ProcessController.respond(["backstory", "group", "set", "work", ungroupedProjectKey], { stdout: "", stderr: "", exitCode: 0 })
    clickByTooltip("Set group")
    wait(10)

    // A successful `group set` also triggers Panel's own onChanged ->
    // refresh() (re-fetch this-week, so rows regroup immediately) — so the
    // group-set run is not necessarily ProcessController's LAST recorded
    // run; search by argv instead.
    var wantKey = ProcessController.keyFor(Launchers.groupSetCommand("work", ungroupedProjectKey))
    var found = null
    for (var i = 0; i < ProcessController.runs.length; i++) {
      if (ProcessController.keyFor(ProcessController.runs[i].command) === wantKey) found = ProcessController.runs[i]
    }
    verify(found !== null, "expected a `group set` run among " + JSON.stringify(ProcessController.runs))
    compare(JSON.stringify(found.command), JSON.stringify(["backstory", "group", "set", "work", ungroupedProjectKey]))
  }

  function test_failed_group_set_shows_stderr() {
    openGroupEditor()
    pickUngroupedProject()
    typeGroupName("work")

    ProcessController.respond(["backstory", "group", "set", "work", ungroupedProjectKey],
      { stdout: "", stderr: "project is already in group other-thing", exitCode: 1 })
    clickByTooltip("Set group")
    wait(10)

    var texts = TestUtil.collectVisibleTexts(panel.testContentItem)
    verify(texts.indexOf("project is already in group other-thing") !== -1,
      "expected stderr text rendered in the editor; got " + JSON.stringify(texts))
  }
}
