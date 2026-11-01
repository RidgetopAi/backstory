import QtQuick
import QtTest
import Quickshell
import Quickshell.Io
import "testutil.js" as TestUtil
import "../../js/launchers.js" as Launchers
import "../../js/glyphs.js" as Glyphs

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

  Component {
    id: editorComponent
    Loader { source: Qt.resolvedUrl("../../GroupEditor.qml") }
  }

  property var loader: null
  property var panel: null

  TestActions { id: act; testCase: testCase }

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
    act.launchedNext(function (r) { return ProcessController.keyFor(r.command) === ProcessController.keyFor(Launchers.thisWeekCommand()) }, "initial this-week fetch")
    tryCompare(panel.view, "loading", false, 2000)
    act.drain()
  }

  function cleanup() {
    if (loader) loader.destroy()
    loader = null
    panel = null
  }

  function clickByTooltip(tooltipText) {
    act.clickWhenReady(function () {
      return TestUtil.findFirst(panel.testContentItem, function (node) {
        return node.tooltipText === tooltipText
      })
    }, { desc: "PanelActionButton with tooltipText " + JSON.stringify(tooltipText) })
  }

  function isGroupList(r) { return ProcessController.keyFor(r.command) === ProcessController.keyFor(Launchers.groupListCommand()) }

  function openGroupEditor() {
    var fetched = ProcessController.runs.filter(isGroupList).length
    clickByTooltip("Groups")
    act.settled(function () { return panel.view.groupEditorOpen }, "group editor never opened")
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "groupsPage") }, "the slide into Groups never finished")
    // The editor fetches its group list on each open; its rows exist once that lands.
    act.launchedCount(isGroupList, fetched + 1, "group list fetch on open")
  }

  function goBack() {
    act.clickWhenReady(function () { return TestUtil.findFirst(panel.testContentItem, function (n) { return n.tooltipText === "Back" }) },
      { desc: "Back", until: function () { return !panel.view.groupEditorOpen } })
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "mainPage") }, "main never slid back")
  }

  // A project grouped in a terminal shows up the next time Groups opens.
  function test_groups_fetches_its_list_each_time_it_opens() {
    openGroupEditor()
    act.settled(function () { return findRow(ungroupedProjectKey) !== null }, "the first list never showed")
    goBack()
    ProcessController.respond(Launchers.groupListCommand(), {
      stdout: JSON.stringify({ groups: [{ group: "tools" }], ungrouped: [], projects: [{ key: "a", display_name: "a", group: "tools" }] }), stderr: "", exitCode: 0
    })
    openGroupEditor()
    act.settled(function () { return findRow("a") !== null && findRow(ungroupedProjectKey) === null }, "Groups showed the list from its last opening")
  }

  // The pick, the typed name and an error are one opening's.
  function test_groups_opens_without_the_last_openings_pick_name_or_error() {
    openGroupEditor()
    pickUngroupedProject()
    typeGroupName("work")
    ProcessController.respond(Launchers.groupSetCommand("work", ungroupedProjectKey), { stdout: "", stderr: "no such thing", exitCode: 1 })
    clickByTooltip("Set group")
    act.settled(function () { return findGroupEditor().errorText === "no such thing" }, "the error never showed")
    goBack()
    openGroupEditor()
    var editor = findGroupEditor()
    compare(editor.selectedProjectKey, "", "the pick carried over")
    compare(editor.newGroupName, "", "the name carried over")
    compare(groupField().text, "", "the field kept the name")
    compare(editor.errorText, "", "the error carried over")
    act.moveAway(panel.testContentItem)
  }

  function findRow(key) {
    return TestUtil.findFirst(panel.testContentItem, function (node) {
      return node.projectKey === key
    })
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
    act.clickWhenReady(function () { return findRow(ungroupedProjectKey) }, {
      desc: "GroupProjectRow for " + ungroupedProjectKey,
      until: function () { var e = findGroupEditor(); return e !== null && e.selectedProjectKey === ungroupedProjectKey }
    })
  }

  function typeGroupName(name) {
    function findField() {
      return TestUtil.findFirst(panel.testContentItem, function (node) {
        return node.placeholderText === "group name"
      })
    }
    act.settled(function () { return findField() !== null }, "no group-name TextField")
    var field = findField()
    field.text = name
    act.settledEq(field, "text", name)
  }

  function heroStatusText() {
    var n = TestUtil.findFirst(panel.testContentItem, function (node) { return node.objectName === "heroStatus" })
    return n === null ? null : n.text
  }

  // Waits for the header's status line, then compares it so a failure shows what it read.
  function expectHeroStatus(text) {
    for (var i = 0; i < 100 && heroStatusText() !== text; i++) wait(20)
    compare(heroStatusText(), text)
  }

  function visibleRules() {
    return TestUtil.findAll(panel.testContentItem, function (n) { return n.visible && n.strength !== undefined })
  }

  function test_a_rule_before_each_group_the_ungrouped_list_and_the_form() {
    ProcessController.respond(Launchers.groupListCommand(), {
      stdout: JSON.stringify({
        groups: [{ group: "tools" }],
        ungrouped: ["b"],
        projects: [{ key: "a", display_name: "a", group: "tools" }, { key: "b", display_name: "b", group: "" }]
      }), stderr: "", exitCode: 0
    })
    openGroupEditor()
    findGroupEditor().refresh()
    act.settled(function () { return findRow("b") !== null }, "ungrouped row never rendered")
    compare(visibleRules().length, 3)
  }

  function test_no_rule_for_an_ungrouped_list_that_is_empty() {
    ProcessController.respond(Launchers.groupListCommand(), {
      stdout: JSON.stringify({
        groups: [{ group: "tools" }],
        ungrouped: [],
        projects: [{ key: "a", display_name: "a", group: "tools" }]
      }), stderr: "", exitCode: 0
    })
    openGroupEditor()
    findGroupEditor().refresh()
    act.settled(function () { return findRow("a") !== null && findRow(ungroupedProjectKey) === null }, "the grouped row never replaced the ungrouped one")
    act.drain()
    compare(visibleRules().length, 2)
  }

  function rowBackground(key) {
    return TestUtil.findFirst(findRow(key), function (n) { return n.objectName === "groupRowBackground" })
  }

  function openTwoRows() {
    ProcessController.respond(Launchers.groupListCommand(), {
      stdout: JSON.stringify({
        groups: [{ group: "tools" }],
        ungrouped: ["b"],
        projects: [{ key: "a", display_name: "a", group: "tools" }, { key: "b", display_name: "b", group: "" }]
      }), stderr: "", exitCode: 0
    })
    openGroupEditor()
    findGroupEditor().refresh()
    act.settled(function () { return findRow("b") !== null }, "ungrouped row never rendered")
    act.quiescent(function () { return [findRow("a").mapToItem(testCase, 0, 0).y, findRow("b").mapToItem(testCase, 0, 0).y] }, "rows never stopped moving")
  }

  function pickedMark(key) {
    return TestUtil.findFirst(findRow(key), function (n) { return n.objectName === "pickedMark" })
  }

  function test_keys_move_one_highlight_and_enter_picks() {
    openTwoRows()
    panel.testKeyItem.forceActiveFocus()
    verify(findRow("a").current, "the first row is not highlighted on open")
    compare(rowBackground("a").border.width, 1, "the highlight is not drawn like the main view's")
    act.pressKey(Qt.Key_J)
    act.settled(function () { return findRow("b").current && !findRow("a").current }, "j never moved the highlight down")
    act.pressKey(Qt.Key_Return)
    act.settledEq(findGroupEditor(), "selectedProjectKey", "b")
    verify(pickedMark("b") !== null, "the picked row shows no check mark")
    act.pressKey(Qt.Key_K)
    act.settled(function () { return findRow("a").current && !findRow("b").current }, "k never moved the highlight up")
    verify(pickedMark("b") !== null, "moving the highlight dropped the pick")
  }

  function test_a_project_with_an_empty_key_is_neither_highlighted_nor_picked_first() {
    // A real store can hold a project with an empty key; it must not pass
    // for "no row highlighted yet". This editor's list holds one.
    ProcessController.respond(Launchers.groupListCommand(), {
      stdout: JSON.stringify({
        groups: [{ group: "tools" }],
        ungrouped: ["", "b"],
        projects: [{ key: "a", display_name: "a", group: "tools" }, { key: "", display_name: "", group: "" },
                   { key: "b", display_name: "b", group: "" }]
      }), stderr: "", exitCode: 0
    })
    var l = editorComponent.createObject(testCase)
    tryCompare(l, "status", Loader.Ready, 2000)
    l.item.shown = true
    act.settledEq(l.item, "cursorKey", "a")
    var picked = TestUtil.findAll(l.item, function (n) { return n.projectKey !== undefined && n.selected === true })
    compare(picked.length, 0, "the empty-key row shows as picked with nothing picked")
    l.destroy()
  }

  function test_hover_moves_the_highlight() {
    openTwoRows()
    act.hover(findRow("b"), { x: findRow("b").width / 4, desc: "row b", until: function () { return findRow("b").current } })
    verify(!findRow("a").current, "two rows highlighted")
    // A cursor left over a row would hover it in the next test.
    act.moveAway(panel.testContentItem)
  }

  // The pointer rests on row b while the keys move up, leave and come
  // back: Groups sliding in under the still pointer isn't a hover.
  function test_rows_sliding_under_a_still_pointer_keep_the_keys_highlight() {
    openTwoRows()
    act.hover(findRow("b"), { x: findRow("b").width / 4, desc: "row b", until: function () { return findRow("b").current } })
    panel.testKeyItem.forceActiveFocus()
    act.pressKey(Qt.Key_K)
    act.settled(function () { return findRow("a").current }, "k never moved the highlight")
    act.pressKey(Qt.Key_U)
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "mainPage") }, "main never slid back")
    act.pressKey(Qt.Key_G)
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "groupsPage") && findRow("b") !== null }, "Groups never came back")
    wait(200)
    verify(findRow("a").current, "the row under the still pointer took the highlight")
    act.moveAway(panel.testContentItem)
  }

  function footerText() {
    var n = TestUtil.findFirst(panel.testContentItem, function (node) { return node.objectName === "footerHint" })
    return n === null ? null : n.text
  }

  function test_footer_lists_the_editors_keys() {
    openGroupEditor()
    compare(footerText(), "j/k move · Enter pick · u back · p pin · Esc close")
  }

  function test_header_and_rule_stay_put_while_the_projects_scroll() {
    var projects = []
    for (var i = 0; i < 40; i++) projects.push({ key: "p" + i, display_name: "project-" + i, group: "" })
    ProcessController.respond(Launchers.groupListCommand(), {
      stdout: JSON.stringify({ groups: [], ungrouped: projects.map(function (p) { return p.key }), projects: projects }), stderr: "", exitCode: 0
    })
    openGroupEditor()
    findGroupEditor().refresh()
    act.settled(function () { return findRow("p39") !== null }, "rows never rendered")
    function y(item) { return item.mapToItem(panel.testContentItem, 0, 0).y }
    act.quiescent(function () { return [y(findRow("p0")), y(findRow("p39")), panel.view.scrollArea.contentHeight] }, "rows never stopped moving")
    var title = TestUtil.findFirst(panel.testContentItem, function (n) { return n.text === "Groups" })
    var rule = visibleRules()[0]
    var titleY = y(title), ruleY = y(rule), firstY = y(findRow("p0"))
    var f = panel.view.scrollArea
    f.contentY = f.contentHeight - f.height
    act.quiescent(function () { return [f.contentY, y(title), y(findRow("p0"))] }, "never stopped scrolling")
    verify(Math.abs(y(title) - titleY) < 0.5, "the header scrolled: " + titleY + " -> " + y(title))
    verify(Math.abs(y(rule) - ruleY) < 0.5, "the rule under the header scrolled: " + ruleY + " -> " + y(rule))
    verify(y(findRow("p0")) < firstY - 100, "the projects did not scroll")
  }

  function test_g_opens_groups_from_the_main_view() {
    verify(panel.view.footerHint.indexOf("g groups") >= 0, "the main footer never listed g: " + panel.view.footerHint)
    panel.testKeyItem.forceActiveFocus()
    act.pressKey(Qt.Key_G)
    act.settledEq(panel.view, "groupEditorOpen", true)
  }

  function test_u_goes_back_to_the_main_view() {
    openGroupEditor()
    panel.testKeyItem.forceActiveFocus()
    act.pressKey(Qt.Key_U)
    act.settledEq(panel.view, "groupEditorOpen", false)
  }

  // A u typed into the group name is text, not back.
  function test_u_in_the_group_name_is_text() {
    openGroupEditor()
    var field = TestUtil.findFirst(panel.testContentItem, function (n) { return n.placeholderText === "group name" })
    verify(field !== null, "no group-name TextField")
    field.forceActiveFocus()
    act.pressKey(Qt.Key_U)
    act.settledEq(field, "text", "u")
    act.drain()
    compare(panel.view.groupEditorOpen, true, "u in the group name went back to the main view")
  }

  function groupField() {
    return TestUtil.findFirst(panel.testContentItem, function (n) { return n.placeholderText === "group name" })
  }

  function isGroupSet(r) { return r.command[1] === "group" && r.command[2] === "set" }

  // Enter in the group name is the form's: with nothing picked it neither
  // picks the highlighted project nor sets a group.
  function test_enter_in_the_group_name_does_not_pick_the_highlighted_project() {
    openTwoRows()
    act.clickWhenReady(groupField, { desc: "group-name field", until: function () { return groupField().activeFocus } })
    act.pressKey(Qt.Key_W)
    act.pressKey(Qt.Key_Return)
    act.settledEq(groupField(), "text", "w")
    act.drain()
    compare(findGroupEditor().selectedProjectKey, "", "Enter in the group name picked the highlighted project")
    compare(ProcessController.runs.filter(isGroupSet).length, 0, "Enter set a group with nothing picked")
  }

  // With a project picked and a name typed, Enter in the field adds the
  // group, as the + does.
  function test_enter_in_the_group_name_adds_the_group() {
    openTwoRows()
    act.clickWhenReady(function () { return findRow("b") }, { desc: "row b", until: function () { return findGroupEditor().selectedProjectKey === "b" } })
    act.clickWhenReady(groupField, { desc: "group-name field", until: function () { return groupField().activeFocus } })
    act.pressKey(Qt.Key_W)
    act.settledEq(groupField(), "text", "w")
    act.pressKey(Qt.Key_Enter)
    var run = act.launchedNext(isGroupSet, "group set")
    compare(JSON.stringify(run.command), JSON.stringify(Launchers.groupSetCommand("w", "b")))
    act.moveAway(panel.testContentItem)
  }

  // Back from Groups after typing a name: the main view's keys work again
  // and type nothing into the hidden field.
  function test_back_from_the_group_name_gives_the_keys_back_to_the_main_view() {
    function row(k) { return { project: { project_key: k, display_name: k, cwd: "/x/" + k, last_activity: "2026-01-01T00:00:00Z" } } }
    ProcessController.respond(Launchers.thisWeekCommand(), {
      stdout: JSON.stringify({ attention: [], week: [], where_left_off: [row("a"), row("b"), row("c")] }), stderr: "", exitCode: 0
    })
    panel.view.refresh()
    act.settled(function () { return panel.view.navRows.length === 3 }, "the rows never loaded")
    openGroupEditor()
    act.clickWhenReady(groupField, { desc: "group-name field", until: function () { return groupField().activeFocus } })
    act.pressKey(Qt.Key_W)
    act.settledEq(groupField(), "text", "w")
    var field = groupField()
    act.clickWhenReady(function () { return TestUtil.findFirst(panel.testContentItem, function (n) { return n.tooltipText === "Back" }) },
      { desc: "Back", until: function () { return !panel.view.groupEditorOpen } })
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "mainPage") }, "main never slid back")
    var before = panel.view.selectedIndex
    act.pressKey(Qt.Key_J)
    act.settled(function () { return panel.view.selectedIndex === before + 1 }, "j did nothing after Back from the group name")
    compare(field.text, "w", "the main view's keys typed into the hidden field")
    act.moveAway(panel.testContentItem)
  }

  function test_header_is_back_then_groups_then_the_counts() {
    openGroupEditor()
    expectHeroStatus("0 groups · 1 project this week")
    var t = TestUtil.collectVisibleTexts(panel.testContentItem)
    var i = t.indexOf("Groups")
    compare(t.slice(i - 1, i + 2), [Glyphs.back(), "Groups", "0 groups · 1 project this week"])
  }

  function test_header_says_one_group() {
    ProcessController.respond(Launchers.groupListCommand(), {
      stdout: JSON.stringify({
        groups: [{ group: "tools" }],
        ungrouped: [],
        projects: [{ key: "a", display_name: "a", group: "tools" }, { key: "b", display_name: "b", group: "tools" }]
      }), stderr: "", exitCode: 0
    })
    openGroupEditor()
    expectHeroStatus("1 group · 2 projects this week")
  }

  function test_add_group_runs_backstory_group_set() {
    openGroupEditor()
    pickUngroupedProject()
    typeGroupName("work")

    ProcessController.respond(["backstory", "group", "set", "work", ungroupedProjectKey], { stdout: "", stderr: "", exitCode: 0 })
    clickByTooltip("Set group")

    // A successful `group set` also triggers Panel's own onChanged ->
    // refresh() (re-fetch this-week, so rows regroup immediately) — so the
    // group-set run is not necessarily ProcessController's LAST recorded
    // run; search by argv instead.
    var wantKey = ProcessController.keyFor(Launchers.groupSetCommand("work", ungroupedProjectKey))
    var found = act.launchedNext(function (r) { return ProcessController.keyFor(r.command) === wantKey }, "group set")
    compare(JSON.stringify(found.command), JSON.stringify(["backstory", "group", "set", "work", ungroupedProjectKey]))
  }

  function test_failed_group_set_shows_stderr() {
    openGroupEditor()
    pickUngroupedProject()
    typeGroupName("work")

    ProcessController.respond(["backstory", "group", "set", "work", ungroupedProjectKey],
      { stdout: "", stderr: "project is already in group other-thing", exitCode: 1 })
    clickByTooltip("Set group")

    act.settled(function () {
      return TestUtil.collectVisibleTexts(panel.testContentItem).indexOf("project is already in group other-thing") !== -1
    }, "expected stderr text rendered in the editor; got " + JSON.stringify(TestUtil.collectVisibleTexts(panel.testContentItem)))
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
    act.settled(function () { return findRow(gitKey) !== null }, "no GroupProjectRow for " + gitKey)
    var row = findRow(gitKey)
    act.settledEq(row, "displayName", "projects/omarcade")
    act.settled(function () { return TestUtil.collectVisibleTexts(panel.testContentItem).indexOf("projects/omarcade") !== -1 }, "projects/omarcade never rendered")

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
    function projectRows() {
      return TestUtil.findAll(panel.testContentItem, function (node) {
        return node.projectKey !== undefined
      })
    }
    act.settled(function () { return projectRows().length === 2 && findRow(workspaceKey) !== null && findRow(outsideKey) !== null }, "rows never settled to the two `projects` entries")
    act.drain()
    var rows = projectRows()
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
