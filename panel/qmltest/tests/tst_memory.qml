import QtQuick
import QtTest
import Quickshell
import Quickshell.Io
import "testutil.js" as TestUtil
import "../../js/launchers.js" as Launchers
import "../../js/memory.js" as MemoryJs
import "../../js/glyphs.js" as Glyphs

// The Memory view suite (task 04d28c19, decision 02c511b3 D5): drives the
// real Panel.qml click path — a project row's Memory action -> MemoryView —
// against the committed `records --json` goldens, an empty payload and a
// failing command, asserting rows, tier/status marks, recorded argv for
// Edit / Delete / each Forget option (dry-run first, --yes only after
// confirm), stderr on failure, and that every visible item stays inside the
// PanelWindow's bounds.
TestCase {
  id: testCase
  name: "MemoryView"
  when: windowShown
  visible: true
  width: 900
  height: 900

  readonly property string projectKey: "acme-widgets"
  readonly property string cwd: "/home/x/acme"
  readonly property string recordsGolden: Qt.resolvedUrl("../../../cmd/backstory/testdata/records/default.json.golden")
  readonly property string historyGolden: Qt.resolvedUrl("../../../cmd/backstory/testdata/records/history.json.golden")

  Component {
    id: panelComponent
    Loader { source: Qt.resolvedUrl("../../Panel.qml") }
  }

  property var loader: null
  property var panel: null

  TestActions { id: act; testCase: testCase }

  function readFixture(url) {
    var xhr = new XMLHttpRequest()
    xhr.open("GET", url, false)
    xhr.send()
    if (xhr.status !== 200) fail("could not read fixture " + url + ": status " + xhr.status)
    return xhr.responseText
  }

  function init() {
    ProcessController.reset()
    Quickshell.reset()
    ProcessController.respond(Launchers.thisWeekCommand(), {
      stdout: JSON.stringify({
        attention: [], week: [],
        where_left_off: [{ project: { project_key: projectKey, display_name: "acme", cwd: "/home/x/acme", last_activity: "2026-01-01T00:00:00Z" } }]
      }), stderr: "", exitCode: 0
    })
    loader = panelComponent.createObject(testCase)
    tryCompare(loader, "status", Loader.Ready, 2000)
    panel = loader.item
    panel.testContentItem.parent = loader
  }

  function cleanup() {
    if (loader) loader.destroy()
    loader = null
    panel = null
  }

  function respondRecords(text, history) {
    ProcessController.respond(Launchers.recordsCommand(projectKey, cwd, !!history), { stdout: text, stderr: "", exitCode: 0 })
  }

  function clickByTooltip(tooltipText, opts) {
    act.clickWhenReady(function () {
      return TestUtil.findFirst(panel.testContentItem, function (node) { return node.tooltipText === tooltipText })
    }, Object.assign({ desc: "button with tooltipText " + JSON.stringify(tooltipText) }, opts))
  }

  function clickByLabel(label, opts) {
    act.clickWhenReady(function () {
      return TestUtil.findFirst(panel.testContentItem, function (node) { return node.label === label })
    }, Object.assign({ desc: "button labelled " + JSON.stringify(label) }, opts))
  }

  function isRecordsRun(history) {
    var key = ProcessController.keyFor(Launchers.recordsCommand(projectKey, cwd, !!history))
    return function (r) { return ProcessController.keyFor(r.command) === key }
  }

  // Open the panel, click the row's Memory action, and wait for the view's
  // `records --json` run to land and the rows to stop reflowing.
  function openMemory(recordsText) {
    respondRecords(recordsText)
    panel.open("{}")
    tryCompare(panel.view, "loading", false, 2000)
    clickByTooltip("Memory", { until: function () { return panel.view.memoryOpen } })
    act.launchedNext(isRecordsRun(false), "memory records run")
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "memoryPage") }, "the slide into Memory never finished")
    act.quiescent(function () { return [texts(), panel.testImplicitHeight] }, "memory view never finished rendering")
  }

  function openedLabelRowMemoryReady() {
    act.quiescent(function () { return [texts(), panel.testImplicitHeight] }, "memory view never finished rendering")
  }

  function expectNoOutOfBounds(label) {
    var bad = []
    act.settled(function () {
      bad = TestUtil.collectOutOfBounds(panel.testContentItem, panel.testContentItem,
        panel.testImplicitWidth, panel.testImplicitHeight)
      return bad.length === 0
    }, label + ": item(s) out of bounds: " + JSON.stringify(bad).slice(0, 2000))
  }

  function expectText(text) {
    act.settled(function () { return texts().indexOf(text) !== -1 }, "text " + JSON.stringify(text) + " never rendered; got " + JSON.stringify(texts()))
  }

  function expectNoText(text) {
    act.drain()
    verify(texts().indexOf(text) === -1, "text " + JSON.stringify(text) + " rendered: " + JSON.stringify(texts()))
  }

  function pinClock() {
    var view = TestUtil.findFirst(panel.testContentItem, function (n) { return typeof n.previewPurge === "function" })
    verify(view !== null, "no MemoryView")
    view.now = function () { return Date.parse("2026-03-10T15:30:45Z") }
  }

  function texts() { return TestUtil.collectVisibleTexts(panel.testContentItem) }

  function runsMatching(argv) {
    var key = ProcessController.keyFor(argv)
    return ProcessController.runs.filter(function (r) { return ProcessController.keyFor(r.command) === key })
  }

  function test_golden_rows_tier_and_status_marks() {
    var text = readFixture(recordsGolden)
    var golden = JSON.parse(text)
    openMemory(text)

    function recordRows() { return TestUtil.findAll(panel.testContentItem, function (n) { return n.rid !== undefined }) }
    act.settled(function () { return recordRows().length === golden.records.length }, "expected " + golden.records.length + " record rows, have " + recordRows().length)
    var rows = recordRows()

    expectText("You")
    var t = texts()
    verify(t.indexOf("You") !== -1, "no You tier label: " + JSON.stringify(t))
    verify(t.indexOf("Agent") !== -1, "no Agent tier label")
    verify(t.indexOf("stale") !== -1, "possibly-stale not marked stale")
    verify(t.indexOf("expired") !== -1, "expired not marked")
    verify(t.indexOf("current") === -1, "a current record must show no status mark")
    // first line only until clicked
    verify(t.indexOf("A current note.") !== -1)
    verify(t.indexOf("A current note.\nSecond line.") === -1)
    var last = rows[rows.length - 1]
    act.clickWhenReady(last, { y: 4, desc: "last record row",
      until: function () { return texts().indexOf("A current note.\nSecond line.") !== -1 } })
  }

  // The device chip shows for exactly the records whose origin_device_name
  // is non-null (the golden's one imported note, from "laptop").
  function test_device_chip_only_for_imported_records() {
    var text = readFixture(recordsGolden)
    var golden = JSON.parse(text)
    var imported = golden.records.filter(function (r) { return r.origin_device_name !== null })
    compare(imported.length, 1)
    openMemory(text)
    function deviceChips() { return TestUtil.findAll(panel.testContentItem, function (n) { return n.objectName === "recordDeviceChip" && n.visible }) }
    act.settled(function () { return deviceChips().length === 1 }, "expected one record device chip")
    var chips = deviceChips()
    var labels = TestUtil.findAll(chips[0], function (n) { return n.objectName === "deviceChipText" })
    compare(labels[0].text, "from " + imported[0].origin_device_name)
  }

  function test_history_toggle_reruns_with_history_and_marks_deleted() {
    var text = readFixture(historyGolden)
    openMemory(readFixture(recordsGolden))
    respondRecords(text, true)
    clickByTooltip("Show history")
    act.launchedCount(isRecordsRun(true), 1, "history records")
    compare(runsMatching(Launchers.recordsCommand(projectKey, cwd, true)).length, 1)
    expectText("deleted")
  }

  function test_empty_state() {
    openMemory(JSON.stringify({ project_key: projectKey, records: [] }))
    expectText("Nothing saved for acme yet.")
  }

  function test_records_error_shows_stderr() {
    ProcessController.respond(Launchers.recordsCommand(projectKey, cwd, false), { stdout: "", stderr: "store is locked", exitCode: 1 })
    panel.open("{}")
    tryCompare(panel.view, "loading", false, 2000)
    clickByTooltip("Memory", { until: function () { return panel.view.memoryOpen } })
    expectText("store is locked")
  }

  function test_edit_records_detached_argv() {
    openMemory(readFixture(recordsGolden))
    var id = JSON.parse(readFixture(recordsGolden)).records[0].id
    clickByTooltip("Edit")
    var call = act.detachedNext(function (c) { return JSON.stringify(c.command.slice(-3)) === JSON.stringify(["backstory", "edit", id]) }, "edit")
    compare(JSON.stringify(call.command.slice(-3)), JSON.stringify(["backstory", "edit", id]))
  }

  function test_delete_confirm_forget_runs_delete_yes() {
    openMemory(readFixture(recordsGolden))
    var id = JSON.parse(readFixture(recordsGolden)).records[0].id
    clickByTooltip("Delete")
    expectText("Forget this record?")
    compare(runsMatching(Launchers.deleteCommand(id)).length, 0, "delete ran before confirm")
    clickByLabel("Forget")
    var runs = act.launchedCount(function (r) { return ProcessController.keyFor(r.command) === ProcessController.keyFor(["backstory", "delete", id, "--yes"]) }, 1, "delete --yes")
    compare(runs.length, 1)
    // the list is refreshed afterwards
    act.launchedCount(isRecordsRun(false), 2, "list refresh")
  }

  function test_delete_cancel_records_no_delete() {
    openMemory(readFixture(recordsGolden))
    clickByTooltip("Delete")
    expectText("Forget this record?")
    clickByLabel("Cancel")
    act.settled(function () { return texts().indexOf("Forget this record?") === -1 }, "confirm never closed after Cancel")
    var deletes = ProcessController.runs.filter(function (r) { return r.command[1] === "delete" })
    compare(deletes.length, 0)
  }

  function test_delete_failure_shows_stderr() {
    openMemory(readFixture(recordsGolden))
    var id = JSON.parse(readFixture(recordsGolden)).records[0].id
    ProcessController.respond(Launchers.deleteCommand(id), { stdout: "", stderr: "no such record", exitCode: 1 })
    clickByTooltip("Delete")
    clickByLabel("Forget")
    expectText("no such record")
  }

  function isPurgeRun(r) { return r.command[1] === "purge" }

  function purgeRuns() {
    return ProcessController.runs.filter(function (r) { return r.command[1] === "purge" })
  }

  function test_each_forget_option_dry_runs_then_yes() {
    var scopes = ["Last hour", "Today", "Everything here"]
    for (var i = 0; i < scopes.length; i++) {
      cleanup()
      init()
      openMemory(readFixture(recordsGolden))
      pinClock()
      clickByLabel(scopes[i])
      var dry = act.launchedCount(isPurgeRun, 1, "dry-run " + scopes[i])
      act.drain()
      compare(purgeRuns().length, 1)
      var argv = dry[0].command
      compare(argv[argv.length - 1], "--dry-run")
      compare(argv[3], projectKey)
      compare(argv[4], "--location")
      compare(argv[5], cwd)
      compare(argv.indexOf("--yes"), -1)
      if (scopes[i] === "Everything here") compare(argv.indexOf("--since"), -1)
      else if (scopes[i] === "Last hour") compare(argv[argv.indexOf("--since") + 1], "2026-03-10T14:30:45Z")
      else verify(/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$/.test(argv[argv.indexOf("--since") + 1]), JSON.stringify(argv))

      // No canned stdout was registered (unparseable); register it and redo.
      ProcessController.respond(argv, { stdout: "would purge 3 sessions, 42 events, 5 records\n", stderr: "", exitCode: 0 })
      clickByLabel(scopes[i])
      expectText("Forget 3 sessions (42 events) and 5 saved records from acme? This cannot be undone.")
      var yesArgv = argv.slice(0, argv.length - 1).concat(["--yes"])
      compare(runsMatching(yesArgv).length, 0, "--yes ran before confirm")

      clickByLabel("Forget")
      act.launchedCount(function (r) { return ProcessController.keyFor(r.command) === ProcessController.keyFor(yesArgv) }, 1, "--yes run")
      compare(runsMatching(yesArgv).length, 1)
    }
  }

  function test_forget_cancel_records_no_yes_run() {
    openMemory(readFixture(recordsGolden))
    pinClock()
    clickByLabel("Everything here")
    var argv = act.launchedCount(isPurgeRun, 1, "first dry-run")[0].command
    ProcessController.respond(argv, { stdout: "would purge 1 sessions, 5 events, 0 records\n", stderr: "", exitCode: 0 })
    clickByLabel("Everything here")
    expectText("Forget 1 sessions (5 events) and 0 saved records from acme? This cannot be undone.")
    clickByLabel("Cancel")
    act.settled(function () { return texts().indexOf("Forget 1 sessions (5 events) and 0 saved records from acme? This cannot be undone.") === -1 }, "confirm never closed after Cancel")
    var yes = purgeRuns().filter(function (r) { return r.command.indexOf("--yes") !== -1 })
    compare(yes.length, 0)
  }

  function test_kind_word_on_each_row() {
    var golden = JSON.parse(readFixture(recordsGolden))
    golden.records.push({ id: "aaaa0009-0000-4000-8000-000000000009", ts: "2026-01-01T00:07:00Z", kind: "outcome",
      tier: "agent-declared", status: "current", text: "Swirl approved." })
    openMemory(JSON.stringify(golden))
    expectText("Outcome")
    var t = texts()
    verify(t.indexOf("Outcome") !== -1, "no Outcome kind word: " + JSON.stringify(t))
    verify(t.indexOf("Decision") !== -1, "no Decision kind word")
    verify(t.indexOf("\uf11e") !== -1, "outcome row lacks the flag glyph")
    expectNoOutOfBounds("kind word")
  }

  function test_layout_within_bounds() {
    openMemory(readFixture(recordsGolden))
    // exercise the taller states too: an open delete confirm.
    clickByTooltip("Delete")
    expectText("Forget this record?")
    expectNoOutOfBounds("delete confirm open")
  }

  function visibleRules() {
    return TestUtil.findAll(panel.testContentItem, function (n) { return n.visible && n.strength !== undefined })
  }

  function footerText() {
    var n = TestUtil.findFirst(panel.testContentItem, function (node) { return node.objectName === "footerHint" })
    return n === null ? null : n.text
  }

  function test_footer_lists_memorys_keys() {
    openMemory(readFixture(recordsGolden))
    compare(footerText(), "j/k move · → expand · ← fold · u back · p pin · Esc close")
  }

  function test_u_goes_back_to_the_main_view() {
    openMemory(readFixture(recordsGolden))
    panel.testKeyItem.forceActiveFocus()
    act.pressKey(Qt.Key_U)
    act.settledEq(panel.view, "memoryOpen", false)
  }

  function test_rules_under_the_header_and_above_forget() {
    openMemory(readFixture(recordsGolden))
    var rules = visibleRules()
    compare(rules.length, 2)
    compare(rules[1].objectName, "forgetDivider")
  }

  // ---- Expanded records and history, kept across a pop-out ----
  function recordItem(id) {
    return TestUtil.findFirst(panel.testContentItem, function (n) { return n.rid === id })
  }

  function test_clicking_a_record_expands_it_and_the_snapshot_lists_it() {
    var id = JSON.parse(readFixture(recordsGolden)).records[0].id
    openMemory(readFixture(recordsGolden))
    act.clickWhenReady(function () { return recordItem(id) }, { desc: "first record", until: function () { return recordItem(id).expanded } })
    compare(JSON.stringify(panel.view.memoryView.snapshot()), JSON.stringify({ history: false, expanded: [id] }))
  }

  // ---- Keys: the one highlight, as on the main view ----
  function test_keys_move_the_highlight_and_expand_and_fold_it() {
    var ids = JSON.parse(readFixture(recordsGolden)).records.map(function (r) { return r.id })
    openMemory(readFixture(recordsGolden))
    panel.testKeyItem.forceActiveFocus()
    verify(recordItem(ids[0]).current, "the first record is not highlighted on open")
    act.pressKey(Qt.Key_J)
    act.settled(function () { return recordItem(ids[1]).current && !recordItem(ids[0]).current }, "j never moved the highlight down")
    act.pressKey(Qt.Key_Right)
    act.settled(function () { return recordItem(ids[1]).expanded }, "right never expanded the highlighted record")
    verify(!recordItem(ids[0]).expanded)
    act.pressKey(Qt.Key_Left)
    act.settled(function () { return !recordItem(ids[1]).expanded }, "left never folded it")
    act.pressKey(Qt.Key_K)
    act.settled(function () { return recordItem(ids[0]).current }, "k never moved the highlight up")
    act.pressKey(Qt.Key_Return)
    act.settled(function () { return recordItem(ids[0]).expanded }, "Enter never expanded the highlighted record")
  }

  // Deleting the highlighted last record moves the highlight onto the new
  // last one, and the keys go on acting on it.
  function test_deleting_the_highlighted_last_record_highlights_the_new_last() {
    var golden = JSON.parse(readFixture(recordsGolden))
    var ids = golden.records.map(function (r) { return r.id })
    openMemory(JSON.stringify(golden))
    panel.testKeyItem.forceActiveFocus()
    for (var i = 1; i < ids.length; i++) act.pressKey(Qt.Key_J)
    act.settled(function () { return recordItem(ids[ids.length - 1]).current }, "j never reached the last record")
    respondRecords(JSON.stringify({ records: golden.records.slice(0, -1) }))
    panel.view.memoryView.deleteRecord(ids[ids.length - 1])
    act.settled(function () { return recordItem(ids[ids.length - 1]) === null }, "the deleted record stayed")
    var last = ids[ids.length - 2]
    act.settled(function () { return recordItem(last).current }, "no record highlighted after the last one went")
    act.pressKey(Qt.Key_Return)
    act.settled(function () { return recordItem(last).expanded }, "Enter never expanded the new last record")
  }

  function test_going_back_into_the_same_projects_memory_keeps_the_highlight_and_expanded_records() {
    var ids = JSON.parse(readFixture(recordsGolden)).records.map(function (r) { return r.id })
    openMemory(readFixture(recordsGolden))
    panel.testKeyItem.forceActiveFocus()
    act.pressKey(Qt.Key_J)
    act.pressKey(Qt.Key_J)
    act.pressKey(Qt.Key_Right)
    act.settled(function () { return recordItem(ids[2]).current && recordItem(ids[2]).expanded }, "never highlighted and expanded the third record")
    act.pressKey(Qt.Key_U)
    act.settledEq(panel.view, "memoryOpen", false)
    act.pressKey(Qt.Key_M)
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "memoryPage") && recordItem(ids[2]) !== null }, "Memory never came back")
    verify(recordItem(ids[2]).current, "the highlight went back to the top")
    verify(recordItem(ids[2]).expanded, "the expanded record folded")
  }

  function test_another_projects_memory_starts_at_the_top() {
    var ids = JSON.parse(readFixture(recordsGolden)).records.map(function (r) { return r.id })
    openMemory(readFixture(recordsGolden))
    panel.testKeyItem.forceActiveFocus()
    act.pressKey(Qt.Key_J)
    act.settled(function () { return recordItem(ids[1]).current }, "j never moved the highlight")
    act.pressKey(Qt.Key_U)
    act.settledEq(panel.view, "memoryOpen", false)
    ProcessController.respond(Launchers.recordsCommand("other", "/home/x/other", false), { stdout: readFixture(recordsGolden), stderr: "", exitCode: 0 })
    panel.view.openMemory("other", "other", "/home/x/other")
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "memoryPage") && recordItem(ids[0]) !== null }, "the other project's Memory never opened")
    verify(recordItem(ids[0]).current, "another project kept the highlight")
  }

  // Two folders can share a project key; Memory is per folder, so the other
  // folder starts at the top too (found by review).
  function test_another_folder_of_the_same_project_starts_at_the_top() {
    var ids = JSON.parse(readFixture(recordsGolden)).records.map(function (r) { return r.id })
    openMemory(readFixture(recordsGolden))
    panel.testKeyItem.forceActiveFocus()
    act.pressKey(Qt.Key_J)
    act.settled(function () { return recordItem(ids[1]).current }, "j never moved the highlight")
    act.pressKey(Qt.Key_U)
    act.settledEq(panel.view, "memoryOpen", false)
    ProcessController.respond(Launchers.recordsCommand(projectKey, "/home/x/acme-2", false), { stdout: readFixture(recordsGolden), stderr: "", exitCode: 0 })
    panel.view.openMemory(projectKey, "acme-2", "/home/x/acme-2")
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "memoryPage") && recordItem(ids[0]) !== null }, "the other folder's Memory never opened")
    verify(recordItem(ids[0]).current, "another folder kept the highlight")
  }

  function test_hover_moves_the_highlight() {
    var ids = JSON.parse(readFixture(recordsGolden)).records.map(function (r) { return r.id })
    openMemory(readFixture(recordsGolden))
    act.hover(recordItem(ids[2]), { x: 4, desc: "third record", until: function () { return recordItem(ids[2]).current } })
    verify(!recordItem(ids[0]).current, "two records highlighted")
    act.moveAway(panel.testContentItem)
  }

  // The pointer rests on the third record while the keys move up, leave
  // and come back: Memory sliding in under the still pointer isn't a hover.
  function test_records_sliding_under_a_still_pointer_keep_the_keys_highlight() {
    var ids = JSON.parse(readFixture(recordsGolden)).records.map(function (r) { return r.id })
    openMemory(readFixture(recordsGolden))
    act.hover(recordItem(ids[2]), { x: 4, desc: "third record", until: function () { return recordItem(ids[2]).current } })
    panel.testKeyItem.forceActiveFocus()
    act.pressKey(Qt.Key_K)
    act.settled(function () { return recordItem(ids[1]).current }, "k never moved the highlight")
    act.pressKey(Qt.Key_U)
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "mainPage") }, "main never slid back")
    act.pressKey(Qt.Key_M)
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "memoryPage") && recordItem(ids[1]) !== null }, "Memory never came back")
    wait(200)
    verify(recordItem(ids[1]).current, "the record under the still pointer took the highlight")
    act.moveAway(panel.testContentItem)
  }

  // A click opens Memory with the pointer resting in the main view where a
  // later record will land (no key reset the gate): the records sliding in
  // under it aren't hovers.
  function test_records_sliding_in_under_a_resting_pointer_keep_the_first_highlighted() {
    var ids = JSON.parse(readFixture(recordsGolden)).records.map(function (r) { return r.id })
    respondRecords(readFixture(recordsGolden))
    panel.open("{}")
    tryCompare(panel.view, "loading", false, 2000)
    // The pointer crosses the main view's row first, so the gate has seen it
    // somewhere else, then rests off the rows.
    var row = TestUtil.findFirst(panel.testContentItem, function (n) { return n.summary && n.stripCounts !== undefined })
    act.hover(row, { until: function () { return row.selected } })
    act.hover(panel.testContentItem, { x: 100, y: 140 })
    act.settled(function () { return panel.testContentItem.height > 160 }, "the main view is shorter than the resting spot")
    wait(100)
    panel.view.openMemory(projectKey, "acme", cwd)
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "memoryPage") && recordItem(ids[4]) !== null }, "Memory never opened")
    wait(200)
    var lit = ids.filter(function (id) { return recordItem(id).current })
    compare(JSON.stringify(lit), JSON.stringify([ids[0]]))
    act.moveAway(panel.testContentItem)
  }

  // The pointer leaves a Recent row for the HERE card's Memory button, off
  // the rows, and opens Memory there: the record that slides in under it
  // isn't a hover.
  function test_records_sliding_in_under_the_here_cards_memory_button_keep_the_first_highlighted() {
    var ids = JSON.parse(readFixture(recordsGolden)).records.map(function (r) { return r.id })
    ProcessController.respond(Launchers.thisWeekCommand(), {
      stdout: JSON.stringify({
        here: { project_key: projectKey, display_name: "acme", cwd: cwd, source: "focused" },
        attention: [], week: [],
        where_left_off: [{ project: { project_key: projectKey, display_name: "acme", cwd: cwd, last_activity: "2026-01-01T00:00:00Z" } },
                         { project: { project_key: "b", display_name: "b", cwd: "/home/x/b", last_activity: "2026-01-01T00:00:00Z" } }]
      }), stderr: "", exitCode: 0
    })
    respondRecords(readFixture(recordsGolden))
    openPanelMain()
    var rows = TestUtil.findAll(panel.testContentItem, function (n) { return n.visible && n.summary && n.stripCounts !== undefined })
    var button = TestUtil.findFirst(panel.testContentItem, function (n) { return n.objectName === "hereMemory" })
    act.quiescent(function () { return [rows[1].mapToItem(null, 0, 0).y, button.mapToItem(null, 0, 0).y] }, "the main view never stopped moving")
    act.hover(rows[1], { until: function () { return rows[1].selected }, desc: "the second Recent row" })
    var at = button.mapToItem(null, button.width / 2, button.height / 2)
    act.hover(button)
    act.clickWhenReady(button, { desc: "the HERE card's Memory", until: function () { return panel.view.memoryOpen } })
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "memoryPage") && recordItem(ids[4]) !== null }, "Memory never opened")
    var under = ids.filter(function (id) { var r = recordItem(id); var p = r.mapFromItem(null, at.x, at.y); return p.y >= 0 && p.y < r.height })
    verify(under.length === 1 && under[0] !== ids[0], "no later record slid in under the button: " + JSON.stringify(under))
    wait(200)
    var lit = ids.filter(function (id) { return recordItem(id).current })
    compare(JSON.stringify(lit), JSON.stringify([ids[0]]))
    act.moveAway(panel.testContentItem)
  }

  // ---- Memory slides in once its records are here ----

  // Memory's page while any of it shows (it is hidden while off to the side).
  function memoryPage() {
    return TestUtil.findFirst(panel.testContentItem, function (n) { return n.objectName === "memoryPage" })
  }

  function heldRecords(other) {
    ProcessController.respond(Launchers.recordsCommand(other ? "other" : projectKey, other ? "/home/x/other" : cwd, false),
      { stdout: readFixture(recordsGolden), stderr: "", exitCode: 0, held: true })
  }

  function openPanelMain() {
    panel.open("{}")
    tryCompare(panel.view, "loading", false, 2000)
  }

  function test_memory_waits_for_its_records_before_sliding_in() {
    heldRecords()
    openPanelMain()
    panel.view.openMemory(projectKey, "acme", cwd)
    wait(100)
    compare(memoryPage(), null, "Memory slid in before its records came")
    verify(TestUtil.slideDone(panel.testContentItem, "mainPage"), "the main view moved while Memory waited")
    compare(ProcessController.release(Launchers.recordsCommand(projectKey, cwd, false)), 1)
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "memoryPage") }, "Memory never slid in")
    var ids = JSON.parse(readFixture(recordsGolden)).records.map(function (r) { return r.id })
    verify(recordItem(ids[0]) !== null, "the records are not there")
  }

  // The keys open Memory under a pointer resting on a Recent row; while it
  // shows "Loading records" the pointer moves over the empty page and stops.
  // Records then coming in under it are a list change, not a hover.
  function test_records_coming_in_under_a_pointer_that_moved_while_loading_keep_the_first_highlighted() {
    var ids = JSON.parse(readFixture(recordsGolden)).records.map(function (r) { return r.id })
    ProcessController.respond(Launchers.thisWeekCommand(), {
      stdout: JSON.stringify({
        here: { project_key: projectKey, display_name: "acme", cwd: cwd, source: "focused" },
        attention: [], week: [],
        where_left_off: [{ project: { project_key: projectKey, display_name: "acme", cwd: cwd, last_activity: "2026-01-01T00:00:00Z" } },
                         { project: { project_key: "b", display_name: "b", cwd: "/home/x/b", last_activity: "2026-01-01T00:00:00Z" } }]
      }), stderr: "", exitCode: 0
    })
    heldRecords()
    openPanelMain()
    var rows = TestUtil.findAll(panel.testContentItem, function (n) { return n.visible && n.summary && n.stripCounts !== undefined })
    act.quiescent(function () { return rows.map(function (r) { return r.mapToItem(null, 0, 0).y }) }, "the main view never stopped moving")
    var row = rows[rows.length - 1]
    act.hover(row, { until: function () { return row.selected }, desc: "the last Recent row" })
    var at = row.mapToItem(null, row.width / 2, row.height / 2)
    panel.view.openMemory(projectKey, "acme", cwd)
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "memoryPage") }, "Memory never slid in to wait")
    at = { x: at.x, y: at.y - 50 }
    var rest = panel.testContentItem.mapFromItem(null, at.x, at.y)
    act.hover(panel.testContentItem, { x: rest.x, y: rest.y })
    ProcessController.release(Launchers.recordsCommand(projectKey, cwd, false))
    act.settled(function () { return recordItem(ids[4]) !== null }, "the records never came")
    act.quiescent(function () { return ids.map(function (id) { return recordItem(id).mapToItem(null, 0, 0).y }) }, "the records never stopped moving")
    var under = ids.filter(function (id) { var r = recordItem(id); var p = r.mapFromItem(null, at.x, at.y); return p.y >= 0 && p.y < r.height })
    verify(under.length === 1 && under[0] !== ids[0], "no later record came in under the pointer: " + JSON.stringify(under))
    wait(200)
    var lit = ids.filter(function (id) { return recordItem(id).current })
    compare(JSON.stringify(lit), JSON.stringify([ids[0]]))
    act.moveAway(panel.testContentItem)
  }

  function test_slow_records_slide_in_with_loading_not_the_empty_page() {
    heldRecords()
    openPanelMain()
    panel.view.openMemory(projectKey, "acme", cwd)
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "memoryPage") }, "Memory never slid in without its records")
    verify(texts().indexOf("Loading records\u2026") !== -1, "no loading line: " + JSON.stringify(texts()))
    verify(!texts().some(function (t) { return t.indexOf("Nothing saved") === 0 }), "the empty page showed while loading")
    ProcessController.release(Launchers.recordsCommand(projectKey, cwd, false))
    var ids = JSON.parse(readFixture(recordsGolden)).records.map(function (r) { return r.id })
    act.settled(function () { return recordItem(ids[0]) !== null }, "the records never came")
    verify(texts().indexOf("Loading records\u2026") === -1, "the loading line stayed")
  }

  function test_another_project_never_shows_the_last_ones_records() {
    var ids = JSON.parse(readFixture(recordsGolden)).records.map(function (r) { return r.id })
    openMemory(readFixture(recordsGolden))
    panel.view.showMain()
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "mainPage") }, "main never slid back")
    heldRecords(true)
    panel.view.openMemory("other", "other", "/home/x/other")
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "memoryPage") }, "Memory never slid in")
    verify(recordItem(ids[0]) === null, "the last project's records showed for another")
  }

  // Back out of a Memory still loading, into another folder's: the first
  // run is stopped, and its end (a crash, no output) is not the second
  // folder's records or its error.
  function test_a_superseded_records_run_is_not_the_next_folders() {
    var ids = JSON.parse(readFixture(recordsGolden)).records.map(function (r) { return r.id })
    heldRecords()
    heldRecords(true)
    openPanelMain()
    panel.view.openMemory(projectKey, "acme", cwd)
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "memoryPage") }, "Memory never slid in to wait")
    panel.view.showMain()
    panel.view.openMemory("other", "other", "/home/x/other")
    act.drain()
    verify(!panel.view.memoryView.ready, "the stopped run made the other folder's Memory ready")
    compare(panel.view.memoryView.errorText, "", "the stopped run's end showed as an error")
    compare(ProcessController.release(Launchers.recordsCommand(projectKey, cwd, false)), 0, "the first run was never stopped")
    compare(ProcessController.release(Launchers.recordsCommand("other", "/home/x/other", false)), 1)
    act.settled(function () { return panel.view.memoryView.ready && recordItem(ids[0]) !== null }, "the other folder's records never came")
    compare(panel.view.memoryView.errorText, "")
  }

  // The main view stays while Memory waits for its records; another row's
  // Memory clicked then is the one that opens.
  function test_a_second_memory_while_the_first_waits_opens_the_second() {
    var ids = JSON.parse(readFixture(recordsGolden)).records.map(function (r) { return r.id })
    heldRecords()
    ProcessController.respond(Launchers.recordsCommand("other", "/home/x/other", false),
      { stdout: readFixture(recordsGolden), stderr: "", exitCode: 0 })
    openPanelMain()
    panel.view.openMemory(projectKey, "acme", cwd)
    panel.view.openMemory("other", "other", "/home/x/other")
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "memoryPage") && recordItem(ids[0]) !== null },
      "the second Memory never opened with its records")
    compare(panel.view.memoryView.displayName, "other")
    compare(ProcessController.release(Launchers.recordsCommand(projectKey, cwd, false)), 0, "the first run was left going")
  }

  // The gear on the main view still showing while Memory waits opens
  // Groups instead: one view at a time.
  function test_the_gear_while_memory_waits_opens_groups_alone() {
    heldRecords()
    openPanelMain()
    panel.view.openMemory(projectKey, "acme", cwd)
    clickByTooltip("Groups", { until: function () { return panel.view.groupEditorOpen } })
    verify(!panel.view.memoryOpen, "Memory stayed open under Groups")
    ProcessController.release(Launchers.recordsCommand(projectKey, cwd, false))
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "groupsPage") }, "Groups never slid in")
    wait(400)
    compare(memoryPage(), null, "Memory showed with Groups")
  }

  // A restore (the pop-out) lands in Memory without a slide, also when its
  // records come after restore() returns.
  function test_a_restore_into_memory_lands_without_a_slide_when_its_records_come_late() {
    heldRecords()
    openPanelMain()
    panel.view.restore({ view: "memory", memory: { projectKey: projectKey, displayName: "acme", location: cwd, state: {} } })
    wait(50)
    compare(memoryPage(), null, "Memory came in before its records")
    ProcessController.release(Launchers.recordsCommand(projectKey, cwd, false))
    verify(TestUtil.slideDone(panel.testContentItem, "memoryPage"), "the restored Memory slid in")
    act.drain()
    panel.view.showMain()
    verify(!TestUtil.slideDone(panel.testContentItem, "mainPage"), "going back after the restore did not slide")
  }

  // The same when the records are slow enough that Memory comes in to wait.
  function test_a_restore_into_memory_lands_without_a_slide_when_the_wait_gives_up() {
    heldRecords()
    openPanelMain()
    panel.view.restore({ view: "memory", memory: { projectKey: projectKey, displayName: "acme", location: cwd, state: {} } })
    act.settled(function () { return memoryPage() !== null }, "Memory never came in to wait")
    verify(TestUtil.slideDone(panel.testContentItem, "memoryPage"), "the restored Memory slid in")
    ProcessController.release(Launchers.recordsCommand(projectKey, cwd, false))
  }

  // Left before its records came, the restored Memory gives the slide back.
  function test_a_restore_into_memory_left_while_waiting_slides_again() {
    heldRecords()
    openPanelMain()
    panel.view.restore({ view: "memory", memory: { projectKey: projectKey, displayName: "acme", location: cwd, state: {} } })
    panel.view.helpOpen = true
    act.drain()
    panel.view.showMain()
    verify(!TestUtil.slideDone(panel.testContentItem, "mainPage"), "going back from Help did not slide")
  }

  function test_the_same_folder_slides_in_at_once_with_its_records() {
    var ids = JSON.parse(readFixture(recordsGolden)).records.map(function (r) { return r.id })
    openMemory(readFixture(recordsGolden))
    panel.view.showMain()
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "mainPage") }, "main never slid back")
    heldRecords()
    panel.view.openMemory(projectKey, "acme", cwd)
    wait(100)
    verify(memoryPage() !== null, "Memory waited for a refresh of records it had")
    verify(recordItem(ids[0]) !== null, "the records it had are gone")
  }

  function test_restore_opens_with_history_and_the_record_expanded() {
    var history = readFixture(historyGolden)
    var id = JSON.parse(history).records[0].id
    respondRecords(history, true)
    panel.open("{}")
    tryCompare(panel.view, "loading", false, 2000)
    panel.view.memoryView.restore({ history: true, expanded: [id, 42] })
    clickByTooltip("Memory", { until: function () { return panel.view.memoryOpen } })
    act.launchedNext(isRecordsRun(true), "memory history run")
    act.settled(function () { var r = recordItem(id); return r !== null && r.expanded }, "restored record never shown expanded")
    compare(ProcessController.runs.filter(isRecordsRun(false)).length, 0, "fetched without history first")
  }

  // A restore is for the one opening it came with: another project's
  // Memory after it starts with nothing expanded.
  function test_a_restore_expands_records_for_one_opening_only() {
    var id = JSON.parse(readFixture(recordsGolden)).records[0].id
    respondRecords(readFixture(recordsGolden))
    panel.open("{}")
    tryCompare(panel.view, "loading", false, 2000)
    panel.view.memoryView.restore({ history: false, expanded: [id] })
    clickByTooltip("Memory", { until: function () { return panel.view.memoryOpen } })
    act.settled(function () { var r = recordItem(id); return r !== null && r.expanded }, "restored record never shown expanded")
    panel.view.showMain()
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "mainPage") }, "main never slid back")
    ProcessController.respond(Launchers.recordsCommand("other", "/home/x/other", false), { stdout: readFixture(recordsGolden), stderr: "", exitCode: 0 })
    panel.view.openMemory("other", "other", "/home/x/other")
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "memoryPage") && recordItem(id) !== null }, "the other project's Memory never opened")
    verify(!recordItem(id).expanded, "the restore expanded a record in another project's Memory")
  }

  // ---- The header: back first, the project as the title, then the view
  // and its record count ----
  function test_header_is_back_then_the_project_then_memory_and_the_count() {
    var golden = JSON.parse(readFixture(recordsGolden))
    verify(golden.records.length > 1, "fixture needs several records")
    openMemory(JSON.stringify(golden))
    var t = texts()
    var i = t.indexOf("acme")
    compare(t.slice(i - 1, i + 2), [Glyphs.back(), "acme", "Memory · " + golden.records.length + " saved records"])
  }

  // A long project name is cut with an ellipsis before the header's buttons
  // (found by review: it ran under them).
  function test_a_long_project_name_stops_before_the_header_buttons() {
    openMemory(readFixture(recordsGolden))
    panel.view.openMemory(projectKey, "a-project-name-long-enough-to-run-under-the-header-buttons-and-past-them", cwd)
    var title = TestUtil.findFirst(panel.testContentItem, function (n) { return n.objectName === "heroTitle" && n.text.indexOf("a-project-name") === 0 })
    // The pin is the header's leftmost button in the window.
    var pin = TestUtil.findFirst(panel.testContentItem, function (n) { return n.objectName === "pinButton" && n.mapToItem(panel.testContentItem, 0, 0).y < title.mapToItem(panel.testContentItem, 0, 0).y + 40 })
    verify(pin !== null, "no pin button in Memory's header")
    act.settled(function () { return title.truncated }, "the long title was never cut")
    var titleRight = title.mapToItem(panel.testContentItem, title.width, 0).x
    var buttonsLeft = pin.mapToItem(panel.testContentItem, 0, 0).x
    verify(titleRight <= buttonsLeft, "the title ends at " + titleRight + ", past the buttons at " + buttonsLeft)
  }

  // The status line under it is cut the same way.
  function test_a_long_status_line_stops_before_the_header_buttons() {
    openMemory(readFixture(recordsGolden))
    var status = TestUtil.findFirst(panel.testContentItem, function (n) { return n.objectName === "heroStatus" && n.text.indexOf("Memory") === 0 })
    var header = status
    while (header && header.showBack === undefined) header = header.parent
    verify(header !== null, "no header above Memory's status line")
    header.status = "Memory · a status line long enough to run under the header buttons and past them"
    var pin = TestUtil.findFirst(panel.testContentItem, function (n) { return n.objectName === "pinButton" && n.mapToItem(panel.testContentItem, 0, 0).y < status.mapToItem(panel.testContentItem, 0, 0).y + 40 })
    verify(pin !== null, "no pin button in Memory's header")
    act.settled(function () { return status.truncated }, "the long status line was never cut")
    var statusRight = status.mapToItem(panel.testContentItem, status.width, 0).x
    var buttonsLeft = pin.mapToItem(panel.testContentItem, 0, 0).x
    verify(statusRight <= buttonsLeft, "the status line ends at " + statusRight + ", past the buttons at " + buttonsLeft)
  }

  function test_header_says_one_saved_record() {
    var golden = JSON.parse(readFixture(recordsGolden))
    golden.records = golden.records.slice(0, 1)
    openMemory(JSON.stringify(golden))
    expectText("Memory · 1 saved record")
  }

  // ---- Memory and Forget act on exactly the row (task a9a784ec) ----

  readonly property string wsKey: "workspace:/home/x/projects"
  readonly property string labelCwd: "/home/x/projects/backstory-feedback"
  readonly property var pinnedNow: Date.parse("2026-03-10T15:30:45Z")

  // A label row: its summary cwd is the folder, its project_key the shared
  // workspace key.
  function openLabelRowMemory(recordsText) {
    ProcessController.respond(Launchers.thisWeekCommand(), {
      stdout: JSON.stringify({
        attention: [], week: [],
        where_left_off: [{ project: { project_key: wsKey, display_name: "projects/backstory-feedback", cwd: labelCwd, last_activity: "2026-01-01T00:00:00Z" } }]
      }), stderr: "", exitCode: 0
    })
    ProcessController.respond(Launchers.recordsCommand(wsKey, labelCwd, false), { stdout: recordsText, stderr: "", exitCode: 0 })
    panel.open("{}")
    tryCompare(panel.view, "loading", false, 2000)
    clickByTooltip("Memory", { until: function () { return panel.view.memoryOpen } })
    act.launchedNext(isRecordsRun2(wsKey, labelCwd), "label row records run")
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "memoryPage") }, "the slide into Memory never finished")
    openedLabelRowMemoryReady()
    pinClock()
  }

  function isRecordsRun2(key, location) {
    var want = ProcessController.keyFor(Launchers.recordsCommand(key, location, false))
    return function (r) { return ProcessController.keyFor(r.command) === want }
  }

  function followedBy(argv, flag, value) {
    var i = argv.indexOf(flag)
    return i >= 0 && argv[i + 1] === value
  }

  function test_label_row_scopes_records_and_every_forget_option_by_location() {
    openLabelRowMemory(readFixture(recordsGolden))
    var lists = ProcessController.runs.filter(function (r) { return r.command[1] === "records" })
    verify(lists.length >= 1, "no records run")
    for (var i = 0; i < lists.length; i++)
      verify(followedBy(lists[i].command, "--location", labelCwd), "records argv lacks --location <cwd>: " + JSON.stringify(lists[i].command))

    var scopes = ["Last hour", "Today", "Everything here"]
    for (var j = 0; j < scopes.length; j++) {
      clickByLabel(scopes[j])
      var runs = act.launchedCount(isPurgeRun, j + 1, "purge " + scopes[j])
      var argv = runs[runs.length - 1].command
      verify(followedBy(argv, "--location", labelCwd), scopes[j] + " purge argv lacks --location <cwd>: " + JSON.stringify(argv))
      verify(followedBy(argv, "--project", wsKey), "purge argv lacks the project key")
    }
  }

  function test_forget_last_hour_nothing_then_today_confirm_with_all_three_counts() {
    openLabelRowMemory(readFixture(recordsGolden))
    var hourSince = MemoryJs.FORGET_SCOPES[0].since(pinnedNow)
    var todaySince = MemoryJs.FORGET_SCOPES[1].since(pinnedNow)
    ProcessController.respond(Launchers.purgeCommand(wsKey, labelCwd, hourSince, true), { stdout: "would purge 0 sessions, 0 events, 0 records\n", stderr: "", exitCode: 0 })
    ProcessController.respond(Launchers.purgeCommand(wsKey, labelCwd, todaySince, true), { stdout: "would purge 1 sessions, 27 events, 2 records\n", stderr: "", exitCode: 0 })

    clickByLabel("Last hour")
    expectText("Nothing to forget for last hour.")
    verify(texts().join("|").indexOf("This cannot be undone") === -1, "a confirm showed for 0/0/0")

    clickByLabel("Today")
    var want = "Forget 1 sessions (27 events) and 2 saved records from projects/backstory-feedback? This cannot be undone."
    expectText(want)
    act.settled(function () { return texts().indexOf("Nothing to forget for last hour.") === -1 }, "stale note left behind")
    var yesArgv = Launchers.purgeCommand(wsKey, labelCwd, todaySince, false)
    compare(runsMatching(yesArgv).length, 0, "--yes ran before confirm")
    clickByLabel("Forget")
    act.launchedCount(function (r) { return ProcessController.keyFor(r.command) === ProcessController.keyFor(yesArgv) }, 1, "--yes run")
    compare(runsMatching(yesArgv).length, 1)
  }

  function test_records_only_window_is_not_nothing_to_forget() {
    openLabelRowMemory(readFixture(recordsGolden))
    var since = MemoryJs.FORGET_SCOPES[0].since(pinnedNow)
    ProcessController.respond(Launchers.purgeCommand(wsKey, labelCwd, since, true), { stdout: "would purge 0 sessions, 0 events, 2 records\n", stderr: "", exitCode: 0 })
    clickByLabel("Last hour")
    expectText("Forget 0 sessions (0 events) and 2 saved records from projects/backstory-feedback? This cannot be undone.")
  }

  // ---- the result of a click is scrolled into view ----

  function longPayload() {
    var records = []
    for (var i = 0; i < 40; i++)
      records.push({ id: "bbbb" + ("0000" + i).slice(-4) + "-0000-4000-8000-000000000000", ts: "2026-01-01T00:00:00Z", kind: "note",
        tier: "agent-declared", status: "current", text: "Long record number " + i })
    return JSON.stringify({ project_key: wsKey, records: records })
  }

  function flickable() {
    var f = TestUtil.findFirst(panel.testContentItem, function (n) { return n.contentY !== undefined && n.contentHeight !== undefined })
    verify(f !== null, "no Flickable")
    return f
  }

  function memoryView() {
    return TestUtil.findFirst(panel.testContentItem, function (n) { return typeof n.previewPurge === "function" })
  }

  // The item's rect lies inside the Flickable's visible viewport (mapped
  // into the PanelWindow and compared with its visible height).
  function assertInViewport(item, what) {
    var f = flickable()
    var top = item.mapToItem(panel.testContentItem, 0, 0).y
    var viewTop = f.mapToItem(panel.testContentItem, 0, 0).y
    var viewBottom = viewTop + f.height
    verify(viewBottom <= panel.testImplicitHeight + 0.5, "viewport exceeds the window")
    verify(top >= viewTop - 0.5 && top + item.height <= viewBottom + 0.5,
      what + " outside the visible viewport: y=" + top + " h=" + item.height + " viewport=[" + viewTop + "," + viewBottom + "]")
  }

  function scrollToTop() {
    var f = flickable()
    verify(f.contentHeight > f.height * 1.5, "content is not long enough to need scrolling")
    f.contentY = 0
    act.settledEq(f, "contentY", 0)
  }

  function clickForgetOptionScrolledToTop(label) {
    scrollToTop()
    // Scrolled to the top the option is below the viewport, so the mouse
    // cannot reach it: trigger the click handler itself, once it is ready.
    act.activateWhenReady(function () {
      return TestUtil.findFirst(panel.testContentItem, function (n) { return n.label === label })
    }, { desc: label + " button" })
    settle()
  }

  // Lets the deferred reveal and the layout polish run, so a position read
  // afterwards is the settled one (an un-laid-out item reports y=0, which
  // would satisfy any "in view" check vacuously): waits for the scroll
  // position and content size to stop moving.
  function settle() {
    act.quiescent(function () { var f = flickable(); return [f.contentY, f.contentHeight, panel.testImplicitHeight] }, "view never stopped scrolling/reflowing")
  }

  function findTextStarting(prefix) {
    return TestUtil.findFirst(panel.testContentItem, function (n) {
      return typeof n.text === "string" && n.text.indexOf(prefix) === 0
    })
  }

  // The header and the rule under it stay put while the records scroll.
  function test_header_and_rule_stay_put_while_the_records_scroll() {
    openLabelRowMemory(longPayload())
    function y(item) { return item.mapToItem(panel.testContentItem, 0, 0).y }
    var title = TestUtil.findFirst(panel.testContentItem, function (n) { return n.text === "projects/backstory-feedback" })
    var rule = visibleRules()[0]
    var first = recordItem("bbbb0000-0000-4000-8000-000000000000")
    var titleY = y(title), ruleY = y(rule), firstY = y(first)
    var f = flickable()
    f.contentY = f.contentHeight - f.height
    settle()
    verify(Math.abs(y(title) - titleY) < 0.5, "the header scrolled: " + titleY + " -> " + y(title))
    verify(Math.abs(y(rule) - ruleY) < 0.5, "the rule under the header scrolled: " + ruleY + " -> " + y(rule))
    verify(y(first) < firstY - 100, "the records did not scroll")
    verify(rule.visible && title.visible)
  }

  function test_forget_confirm_is_scrolled_into_view() {
    openLabelRowMemory(longPayload())
    var since = MemoryJs.FORGET_SCOPES[0].since(pinnedNow)
    ProcessController.respond(Launchers.purgeCommand(wsKey, labelCwd, since, true), { stdout: "would purge 1 sessions, 27 events, 2 records\n", stderr: "", exitCode: 0 })
    clickForgetOptionScrolledToTop("Last hour")
    tryVerify(function () { return findTextStarting("Forget 1 sessions") !== null }, 2000)
    var confirmText = findTextStarting("Forget 1 sessions")
    tryVerify(function () {
      var top = confirmText.mapToItem(flickable(), 0, 0).y
      return top >= -0.5 && top + confirmText.height <= flickable().height + 0.5
    }, 2000, "confirm never scrolled into view")
    assertInViewport(confirmText, "the Forget confirm")
    // its buttons too: the confirm's whole block is visible
    var forgetButtons = TestUtil.findAll(panel.testContentItem, function (n) { return n.label === "Forget" })
    verify(forgetButtons.length === 1)
    assertInViewport(forgetButtons[0], "the confirm's Forget button")
  }

  function test_nothing_to_forget_note_is_scrolled_into_view() {
    openLabelRowMemory(longPayload())
    var since = MemoryJs.FORGET_SCOPES[0].since(pinnedNow)
    ProcessController.respond(Launchers.purgeCommand(wsKey, labelCwd, since, true), { stdout: "would purge 0 sessions, 0 events, 0 records\n", stderr: "", exitCode: 0 })
    clickForgetOptionScrolledToTop("Last hour")
    tryVerify(function () { return findTextStarting("Nothing to forget") !== null }, 2000)
    var note = findTextStarting("Nothing to forget")
    tryVerify(function () {
      var top = note.mapToItem(flickable(), 0, 0).y
      return top >= -0.5 && top + note.height <= flickable().height + 0.5
    }, 2000, "note never scrolled into view")
    assertInViewport(note, "the nothing-to-forget note")
  }

  function test_purge_error_is_scrolled_into_view() {
    openLabelRowMemory(longPayload())
    var since = MemoryJs.FORGET_SCOPES[0].since(pinnedNow)
    ProcessController.respond(Launchers.purgeCommand(wsKey, labelCwd, since, true), { stdout: "", stderr: "store is locked", exitCode: 1 })
    // The error renders at the TOP of the view while the Forget options sit
    // at the bottom: scrolled to the bottom (where the click happens on the
    // desk) the error is out of sight until the view scrolls up to it.
    var f = flickable()
    f.contentY = f.contentHeight - f.height
    clickByLabel("Last hour")
    settle()
    tryVerify(function () { return findTextStarting("store is locked") !== null }, 2000)
    var err = findTextStarting("store is locked")
    tryVerify(function () {
      var top = err.mapToItem(flickable(), 0, 0).y
      return top >= -0.5 && top + err.height <= flickable().height + 0.5
    }, 2000, "error never scrolled into view")
    assertInViewport(err, "the error")
  }

  function test_record_delete_confirm_is_scrolled_into_view() {
    openLabelRowMemory(longPayload())
    scrollToTop()
    var lastId = JSON.parse(longPayload()).records[39].id
    memoryView().askDelete(lastId)
    settle()
    tryVerify(function () { return findTextStarting("Forget this record?") !== null }, 2000)
    var confirm = findTextStarting("Forget this record?")
    tryVerify(function () {
      var top = confirm.mapToItem(flickable(), 0, 0).y
      return top >= -0.5 && top + confirm.height <= flickable().height + 0.5
    }, 2000, "delete confirm never scrolled into view")
    assertInViewport(confirm, "the delete confirm")
  }

  // ---- Forget reads as its own section (task e8c5afaf) ----

  // Every descendant of root, visible or not: the purge note and the
  // purge-confirm block only show after a dry-run, but still belong to the
  // forget section.
  function allNodes(root) {
    var out = []
    var stack = [root]
    while (stack.length > 0) {
      var n = stack.pop()
      out.push(n)
      var kids = TestUtil.childrenOf(n)
      for (var i = 0; i < kids.length; i++) stack.push(kids[i])
    }
    return out
  }

  function byName(name) {
    var hits = []
    act.settled(function () {
      hits = allNodes(panel.testContentItem).filter(function (n) { return n.objectName === name })
      return hits.length === 1
    }, "expected exactly one " + name)
    return hits[0]
  }

  function isDescendant(node, ancestor) {
    for (var p = node.parent; p; p = p.parent) if (p === ancestor) return true
    return false
  }

  function test_forget_is_one_section_with_divider() {
    openMemory(readFixture(recordsGolden))
    var section = byName("forgetSection")
    var heading = byName("forgetHeading")
    verify(isDescendant(heading, section), "heading outside the forget section")

    var buttons = TestUtil.findAll(section, function (n) { return n.label !== undefined && n.clicked !== undefined })
    var labels = buttons.filter(function (b) { return b.label !== "Forget" && b.label !== "Cancel" }).map(function (b) { return b.label })
    compare(labels, MemoryJs.FORGET_SCOPES.map(function (s) { return s.label }))
    var all = allNodes(panel.testContentItem)
    var scopeButtons = all.filter(function (n) { return n.label !== undefined && labels.indexOf(n.label) !== -1 && n.clicked !== undefined })
    compare(scopeButtons.length, labels.length)
    for (var i = 0; i < scopeButtons.length; i++) verify(isDescendant(scopeButtons[i], section), scopeButtons[i].label + " outside the forget section")

    var note = all.filter(function (n) { return n.objectName === "forgetNote" })
    compare(note.length, 1)
    verify(isDescendant(note[0], section), "purge note outside the forget section")
    var confirm = all.filter(function (n) { return n.objectName === "forgetConfirm" })
    compare(confirm.length, 1)
    verify(isDescendant(confirm[0], section), "purge confirm outside the forget section")

    // The divider is a sibling between the records block and the section.
    var divider = byName("forgetDivider")
    compare(divider.parent, section.parent)
    var sibs = section.parent.children
    var di = -1, si = -1, ri = -1
    for (var k = 0; k < sibs.length; k++) {
      if (sibs[k] === divider) di = k
      if (sibs[k] === section) si = k
      if (sibs[k].rid !== undefined) ri = k
    }
    verify(ri >= 0 && di > ri, "divider must come after the records list")
    compare(si, di + 1, "divider must sit immediately above the forget section")
    verify(divider.height > 0 && divider.visible)
  }

  function test_forget_heading_matches_section_heading_style() {
    cleanup()
    ProcessController.reset()
    ProcessController.respond(Launchers.thisWeekCommand(), {
      stdout: JSON.stringify({
        attention: [], week: [],
        where_left_off: [{ group: "Work", children: [{ project_key: projectKey, display_name: "acme", cwd: cwd, last_activity: "2026-01-01T00:00:00Z" }] }]
      }), stderr: "", exitCode: 0
    })
    loader = panelComponent.createObject(testCase)
    tryCompare(loader, "status", Loader.Ready, 2000)
    panel = loader.item
    panel.testContentItem.parent = loader
    panel.open("{}")
    tryCompare(panel.view, "loading", false, 2000)
    function groupHeader() { return allNodes(panel.testContentItem).filter(function (n) { return n.text === "Work (1)" }) }
    act.settled(function () { return groupHeader().length === 1 }, "no group section heading")
    var header = groupHeader()
    var wantSize = header[0].font.pixelSize
    var wantColor = header[0].color

    // Open the child's Memory (expand the group first).
    var memory = TestUtil.findFirst(panel.testContentItem, function (n) { return n.tooltipText === "Memory" })
    if (memory === null) {
      act.clickWhenReady(function () { return groupHeader()[0] }, {
        desc: "group heading",
        until: function () { return TestUtil.findFirst(panel.testContentItem, function (n) { return n.tooltipText === "Memory" }) !== null }
      })
    }
    clickByTooltip("Memory", { until: function () { return panel.view.memoryOpen } })
    var heading = byName("forgetHeading")
    compare(heading.font.pixelSize, wantSize)
    compare(heading.color, wantColor)
    verify(heading.font.bold)
  }

  function test_forget_helper_says_this_project() {
    openMemory(readFixture(recordsGolden))
    var helper = byName("forgetHelper")
    verify(isDescendant(helper, byName("forgetSection")))
    verify(helper.text.indexOf("this project") !== -1, helper.text)
  }
}
