import QtQuick
import QtTest
import Quickshell
import Quickshell.Io
import "testutil.js" as TestUtil
import "../../js/launchers.js" as Launchers

// The Panel.qml integration suite (task 4fe02e30 Part 1 harness): feeds
// the plugin's real Process stub the committed this-week golden, `{}`, and
// an all-empty-arrays payload, asserting the golden's display names render
// (DONE WHEN clause 2); walks the visible item tree with the panel open
// and with the group editor open, asserting every visible item's mapped
// rect lies inside PanelWindow's implicit bounds (DONE WHEN clause 3); and
// asserts the recorded argv for a Where-you-left-off row click (DONE WHEN
// clause 4 — clause 6's own cwd-interpolation/shape assertions live in
// panel_contract_test.go's Go+Node harness; this file exercises the same
// terminalLaunchCommand through the real QML click path).
TestCase {
  id: testCase
  name: "Panel"
  when: windowShown
  visible: true
  width: 900
  height: 900

  readonly property string goldenUrl: Qt.resolvedUrl("../../../cmd/backstory/testdata/thisweek/all.json.golden")

  function readFixture(url) {
    var xhr = new XMLHttpRequest()
    xhr.open("GET", url, false)
    xhr.send()
    if (xhr.status !== 200) {
      fail("could not read fixture " + url + ": status " + xhr.status)
    }
    return xhr.responseText
  }

  readonly property string emptyArraysPayload: JSON.stringify({
    attention: [], where_left_off: [], week: []
  })

  Component {
    id: panelComponent
    Loader { source: Qt.resolvedUrl("../../Panel.qml") }
  }

  property var loader: null
  property var panel: null

  TestActions { id: act; testCase: testCase }

  function init() {
    ProcessController.reset()
    Quickshell.reset()
    loader = panelComponent.createObject(testCase)
    tryCompare(loader, "status", Loader.Ready, 2000)
    verify(loader.item !== null, "Panel.qml failed to load: status=" + loader.status)
    panel = loader.item
    // See Panel.qml's own doc comment on testContentItem: PanelWindow's
    // contentItem is otherwise a disconnected subtree (our stub PanelWindow
    // is a QtObject, not a real QQuickWindow, so nothing parents it into
    // testCase's own shown window) — reparenting it here is what makes
    // a click deliverable to anything inside the panel at all.
    panel.testContentItem.parent = loader
  }

  function cleanup() {
    if (loader) loader.destroy()
    loader = null
    panel = null
  }

  // openWith registers `command`'s canned response, opens the panel (which
  // triggers refresh() -> dataProcess.running = true, synchronously
  // resolved by the Process stub) and returns once data has landed.
  function openWith(payloadText) {
    ProcessController.respond(Launchers.thisWeekCommand(), { stdout: payloadText, stderr: "", exitCode: 0 })
    panel.open("{}")
    tryCompare(panel, "loading", false, 2000)
    // Column/Row (QtQuick's positioners) recompute implicitWidth/Height
    // via a deferred polish pass, not synchronously on every child change
    // (a read right after a data change saw a stale pre-data height), so
    // wait for the rendered texts and height to stop changing.
    act.quiescent(function () { return [TestUtil.collectVisibleTexts(panel.testContentItem), panel.testImplicitHeight] }, "panel never finished rendering")
  }

  function test_renders_golden_display_names() {
    openWith(readFixture(goldenUrl))
    var golden = JSON.parse(readFixture(goldenUrl))

    var names = {}
    var i
    for (i = 0; i < golden.where_left_off.length; i++) {
      var row = golden.where_left_off[i]
      if (row.project) names[row.project.display_name] = true
      if (row.children) {
        for (var j = 0; j < row.children.length; j++) names[row.children[j].display_name] = true
      }
    }

    // Group children are drawn only while their group is expanded; a
    // workspace-keyed row's text is "<dir> · workspace notes".
    panel.toggleGroup("widget-suite")
    var rendered = []
    var missing = []
    function allRendered() {
      rendered = TestUtil.collectVisibleTexts(panel.testContentItem)
      var renderedSet = {}
      for (var k = 0; k < rendered.length; k++) {
        renderedSet[rendered[k]] = true
        renderedSet[rendered[k].replace(/ \u00b7 workspace notes$/, "")] = true
      }
      missing = []
      // Rows drop the leading "projects/" (decision 63ce9687 P4).
      for (var name in names) {
        if (renderedSet[name.replace(/^projects\/(?=.)/, "")] !== true) missing.push(name)
      }
      return missing.length === 0
    }
    act.settled(allRendered, "expected display names to be rendered; missing " + JSON.stringify(missing) + "; got " + JSON.stringify(rendered))
  }

  // Needs You's Dismiss on a stale-handoff row runs `backstory affirm <id>`
  // for THAT handoff (and only that row has the button).
  function test_needs_you_dismiss_runs_affirm_for_that_handoff() {
    openWith(JSON.stringify({
      attention: [
        { kind: "possibly-stale-handoff", project_key: "wobble-party", reason: "wobble-party: handoff possibly stale \u2014 cargo test failed (exit 101) after it", handoff_id: "h-wobble", evidence_ids: ["7"] },
        { kind: "expired-claim", project_key: "wobble-party", reason: "claim c1 expired with no outcome recorded", evidence_ids: ["c1"] }
      ],
      where_left_off: [], week: []
    }))
    function dismissButtons() {
      return TestUtil.findAll(panel.testContentItem, function (n) { return n.objectName === "attentionDismiss" && n.visible })
    }
    act.settled(function () { return dismissButtons().length === 1 }, "expected exactly one visible Dismiss button")
    ProcessController.reset()
    ProcessController.respond(Launchers.thisWeekCommand(), { stdout: emptyArraysPayload, stderr: "", exitCode: 0 })
    act.clickWhenReady(function () { var b = dismissButtons(); return b.length === 1 ? b[0] : null }, { desc: "attention Dismiss" })
    var affirmJson = JSON.stringify(Launchers.affirmCommand("h-wobble"))
    act.launchedNext(function (r) { return JSON.stringify(r.command) === affirmJson }, "affirm")
    compare(JSON.stringify(Launchers.affirmCommand("h-wobble")), JSON.stringify(["backstory", "affirm", "h-wobble"]))
    tryCompare(panel, "loading", false, 2000)
    compare(panel.attentionItems.length, 0)
  }

  function test_handles_empty_object_payload() {
    openWith("{}")
    compare(panel.attentionItems.length, 0)
    compare(panel.whereLeftOffRows.length, 0)
    compare(panel.weekDays.length, 0)
  }

  function test_handles_all_empty_arrays_payload() {
    openWith(emptyArraysPayload)
    compare(panel.attentionItems.length, 0)
    compare(panel.whereLeftOffRows.length, 0)
    compare(panel.weekDays.length, 0)
  }

  function assertLayoutWithinBounds(label) {
    // The polish pass lands a few ticks after a data change: wait for the
    // bounds to hold rather than reading them once.
    var outOfBounds = []
    act.settled(function () {
      outOfBounds = TestUtil.collectOutOfBounds(
        panel.testContentItem, panel.testContentItem,
        panel.testImplicitWidth, panel.testImplicitHeight)
      return outOfBounds.length === 0
    }, label + ": item(s) out of PanelWindow bounds "
      + "(" + panel.testImplicitWidth + "x" + panel.testImplicitHeight + "): "
      + JSON.stringify(outOfBounds).slice(0, 2000))
  }

  function test_layout_within_bounds_panel_open() {
    openWith(readFixture(goldenUrl))
    compare(panel.groupEditorOpen, false)
    assertLayoutWithinBounds("panel open, group editor closed")
  }

  function test_layout_within_bounds_group_editor_open() {
    openWith(readFixture(goldenUrl))
    ProcessController.respond(Launchers.groupListCommand(), { stdout: JSON.stringify({ groups: [], ungrouped: [] }), stderr: "", exitCode: 0 })
    panel.groupEditorOpen = true
    act.settled(function () { return panel.groupEditorOpen }, "group editor never opened")
    assertLayoutWithinBounds("group editor open")
  }

  function test_row_click_opens_terminal_at_cwd() {
    openWith(readFixture(goldenUrl))
    var golden = JSON.parse(readFixture(goldenUrl))
    var target = null
    for (var i = 0; i < golden.where_left_off.length; i++) {
      if (golden.where_left_off[i].project) { target = golden.where_left_off[i].project; break }
    }
    verify(target !== null, "fixture assumption broke: golden has no standalone project row")

    function findRow() {
      return TestUtil.findFirst(panel.testContentItem, function (node) {
        return node.summary && node.summary.cwd === target.cwd
      })
    }
    act.settled(function () { return findRow() !== null }, "could not find a rendered row for cwd " + target.cwd)

    var want = Launchers.terminalLaunchCommand(target.cwd)
    act.clickWhenReady(findRow, { desc: "row for " + target.cwd })
    var last = act.detachedNext(function (c) { return JSON.stringify(c.command) === JSON.stringify(want) }, "row click terminal")
    compare(JSON.stringify(last.command), JSON.stringify(want))
    compare(last.workingDirectory, target.cwd)
  }
}
