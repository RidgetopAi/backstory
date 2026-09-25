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
// a stubbed non-zero `group set` exit shows its stderr in the editor (the
// Part 1 harness spec's own "a stubbed non-zero group exit shows its
// stderr in the editor", DONE WHEN clause 4).
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
      stdout: JSON.stringify({
        groups: [],
        ungrouped: [ungroupedProjectKey],
        projects: [{ key: ungroupedProjectKey, display_name: "acme-week-main", group: "" }]
      }), stderr: "", exitCode: 0
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

  // findGroupEditor locates the embedded GroupEditor instance itself (only
  // reachable once it is visible — walkVisible stops at an invisible
  // ancestor, so this must run after openGroupEditor()), identified by its
  // own refresh()/groupsData members rather than an objectName, since
  // Panel.qml exposes no id for it.
  function findGroupEditor() {
    return TestUtil.findFirst(panel.testContentItem, function (node) {
      return typeof node.refresh === "function" && node.groupsData !== undefined
    })
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

  // test_group_row_shows_display_name_from_projects_array is DONE WHEN
  // clause (3): the group editor renders display_name from `group list
  // --json`'s own `projects` array, never a name derived from the key's
  // own shape. This is the round 5 desk defect's exact reproduction: a git
  // project_key ("<git common dir>|<remote URL>") whose last path segment
  // is the remote URL's basename ("omarcade.git") must still render as the
  // store-computed "projects/omarcade".
  function test_group_row_shows_display_name_from_projects_array() {
    openGroupEditor()

    var gitKey = "/home/brian/projects/omarcade/.git|git@github.com:example/omarcade.git"
    ProcessController.respond(Launchers.groupListCommand(), {
      stdout: JSON.stringify({
        groups: [],
        ungrouped: [gitKey],
        projects: [{ key: gitKey, display_name: "projects/omarcade", group: "" }]
      }), stderr: "", exitCode: 0
    })

    var editor = findGroupEditor()
    verify(editor !== null, "no embedded GroupEditor instance found")
    editor.refresh()
    wait(20)

    var row = TestUtil.findFirst(panel.testContentItem, function (node) {
      return node.projectKey === gitKey
    })
    verify(row !== null, "no GroupProjectRow for " + gitKey)
    compare(row.displayName, "projects/omarcade")

    var texts = TestUtil.collectVisibleTexts(panel.testContentItem)
    verify(texts.indexOf("projects/omarcade") !== -1,
      "expected rendered text \"projects/omarcade\"; got " + JSON.stringify(texts))
    verify(texts.indexOf("omarcade.git") === -1,
      "rendered text must never be the key-derived \"omarcade.git\"; got " + JSON.stringify(texts))
    verify(texts.indexOf(gitKey) === -1,
      "rendered text must never be the raw project_key; got " + JSON.stringify(texts))
  }

  // test_rows_come_only_from_projects_array_never_ungrouped_raw_keys is
  // task 4fe02e30 round 6 DONE WHEN clause (3): the group editor's rows
  // come ONLY from `group list --json`'s own `projects` array (group
  // membership from its own `group` field) — never from `ungrouped` or
  // `groups[].projects`, which are not deduped against a legacy/workspace
  // key pair for the same folder the way `projects` itself already is
  // (round 6 desk defect A). This feeds a REAL-shaped payload: `ungrouped`
  // still lists BOTH the legacy plain key and its workspace-prefixed key
  // for the same folder (exactly what an un-deduped read would produce),
  // while `projects` already carries just the ONE merged entry — and
  // asserts the rendered project rows are EXACTLY `projects`' own entries:
  // right count, right keys, right display names, and never a row (or any
  // rendered text) for the raw legacy key.
  function test_rows_come_only_from_projects_array_never_ungrouped_raw_keys() {
    openGroupEditor()

    var legacyKey = "/home/brian/projects"
    var workspaceKey = "workspace:/home/brian/projects"
    var outsideKey = "/home/brian/.local/bin"
    ProcessController.respond(Launchers.groupListCommand(), {
      stdout: JSON.stringify({
        groups: [],
        ungrouped: [legacyKey, workspaceKey, outsideKey],
        projects: [
          { key: workspaceKey, display_name: "projects", group: "" },
          { key: outsideKey, display_name: "bin", group: "" }
        ]
      }), stderr: "", exitCode: 0
    })

    var editor = findGroupEditor()
    verify(editor !== null, "no embedded GroupEditor instance found")
    editor.refresh()
    wait(20)

    var rows = TestUtil.findAll(panel.testContentItem, function (node) {
      return node.projectKey !== undefined
    })
    compare(rows.length, 2, "expected exactly one row per `projects` entry (2), got " + JSON.stringify(rows.map(function (r) { return r.projectKey })))

    var byKey = {}
    for (var i = 0; i < rows.length; i++) byKey[rows[i].projectKey] = rows[i].displayName

    verify(byKey[legacyKey] === undefined, "a row rendered for the raw legacy key " + legacyKey + "; rows must come only from `projects`")
    compare(byKey[workspaceKey], "projects")
    compare(byKey[outsideKey], "bin")

    var texts = TestUtil.collectVisibleTexts(panel.testContentItem)
    verify(texts.indexOf(legacyKey) === -1,
      "rendered text must never be the raw legacy key " + legacyKey + "; got " + JSON.stringify(texts))
  }
}
