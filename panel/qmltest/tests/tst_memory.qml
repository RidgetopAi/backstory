import QtQuick
import QtTest
import Quickshell
import Quickshell.Io
import "testutil.js" as TestUtil
import "../../js/launchers.js" as Launchers

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
  readonly property string recordsGolden: Qt.resolvedUrl("../../../cmd/backstory/testdata/records/default.json.golden")
  readonly property string historyGolden: Qt.resolvedUrl("../../../cmd/backstory/testdata/records/history.json.golden")

  Component {
    id: panelComponent
    Loader { source: Qt.resolvedUrl("../../Panel.qml") }
  }

  property var loader: null
  property var panel: null

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
    ProcessController.respond(Launchers.recordsCommand(projectKey, !!history), { stdout: text, stderr: "", exitCode: 0 })
  }

  function clickByTooltip(tooltipText) {
    var button = TestUtil.findFirst(panel.testContentItem, function (node) { return node.tooltipText === tooltipText })
    verify(button !== null, "no button with tooltipText " + JSON.stringify(tooltipText))
    mouseClick(button, button.width / 2, button.height / 2)
    wait(20)
  }

  function clickByLabel(label) {
    var button = TestUtil.findFirst(panel.testContentItem, function (node) { return node.label === label })
    verify(button !== null, "no visible button labelled " + JSON.stringify(label))
    mouseClick(button, button.width / 2, button.height / 2)
    wait(20)
  }

  function openMemory(recordsText) {
    respondRecords(recordsText)
    panel.open("{}")
    tryCompare(panel, "loading", false, 2000)
    wait(20)
    clickByTooltip("Memory")
    verify(panel.memoryOpen)
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

    var rows = TestUtil.findAll(panel.testContentItem, function (n) { return n.rid !== undefined })
    compare(rows.length, golden.records.length)

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
    mouseClick(last, last.width / 2, 4)
    wait(20)
    verify(texts().indexOf("A current note.\nSecond line.") !== -1, "click did not expand the full text")
  }

  function test_history_toggle_reruns_with_history_and_marks_deleted() {
    var text = readFixture(historyGolden)
    openMemory(readFixture(recordsGolden))
    respondRecords(text, true)
    clickByTooltip("Show history")
    compare(runsMatching(Launchers.recordsCommand(projectKey, true)).length, 1)
    verify(texts().indexOf("deleted") !== -1, "deleted status mark missing under --history")
  }

  function test_empty_state() {
    openMemory(JSON.stringify({ project_key: projectKey, records: [] }))
    verify(texts().indexOf("Nothing saved for acme yet.") !== -1, JSON.stringify(texts()))
  }

  function test_records_error_shows_stderr() {
    ProcessController.respond(Launchers.recordsCommand(projectKey, false), { stdout: "", stderr: "store is locked", exitCode: 1 })
    panel.open("{}")
    tryCompare(panel, "loading", false, 2000)
    wait(20)
    clickByTooltip("Memory")
    verify(texts().indexOf("store is locked") !== -1, JSON.stringify(texts()))
  }

  function test_edit_records_detached_argv() {
    openMemory(readFixture(recordsGolden))
    clickByTooltip("Edit")
    var call = Quickshell.lastCall()
    verify(call !== null, "no execDetached call")
    var id = JSON.parse(readFixture(recordsGolden)).records[0].id
    compare(JSON.stringify(call.command.slice(-3)), JSON.stringify(["backstory", "edit", id]))
  }

  function test_delete_confirm_forget_runs_delete_yes() {
    openMemory(readFixture(recordsGolden))
    var id = JSON.parse(readFixture(recordsGolden)).records[0].id
    clickByTooltip("Delete")
    verify(texts().indexOf("Forget this record?") !== -1)
    compare(runsMatching(Launchers.deleteCommand(id)).length, 0, "delete ran before confirm")
    clickByLabel("Forget")
    var runs = runsMatching(["backstory", "delete", id, "--yes"])
    compare(runs.length, 1)
    // the list is refreshed afterwards
    verify(runsMatching(Launchers.recordsCommand(projectKey, false)).length >= 2)
  }

  function test_delete_cancel_records_no_delete() {
    openMemory(readFixture(recordsGolden))
    clickByTooltip("Delete")
    clickByLabel("Cancel")
    var deletes = ProcessController.runs.filter(function (r) { return r.command[1] === "delete" })
    compare(deletes.length, 0)
    verify(texts().indexOf("Forget this record?") === -1)
  }

  function test_delete_failure_shows_stderr() {
    openMemory(readFixture(recordsGolden))
    var id = JSON.parse(readFixture(recordsGolden)).records[0].id
    ProcessController.respond(Launchers.deleteCommand(id), { stdout: "", stderr: "no such record", exitCode: 1 })
    clickByTooltip("Delete")
    clickByLabel("Forget")
    verify(texts().indexOf("no such record") !== -1, JSON.stringify(texts()))
  }

  function purgeRuns() {
    return ProcessController.runs.filter(function (r) { return r.command[1] === "purge" })
  }

  function test_each_forget_option_dry_runs_then_yes() {
    var scopes = ["Last hour", "Today", "Everything in this project"]
    for (var i = 0; i < scopes.length; i++) {
      cleanup()
      init()
      openMemory(readFixture(recordsGolden))
      pinClock()
      clickByLabel(scopes[i])
      var dry = purgeRuns()
      compare(dry.length, 1)
      var argv = dry[0].command
      compare(argv[argv.length - 1], "--dry-run")
      compare(argv[3], projectKey)
      compare(argv.indexOf("--yes"), -1)
      if (scopes[i] === "Everything in this project") compare(argv.indexOf("--since"), -1)
      else if (scopes[i] === "Last hour") compare(argv[argv.indexOf("--since") + 1], "2026-03-10T14:30:45Z")
      else verify(/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$/.test(argv[argv.indexOf("--since") + 1]), JSON.stringify(argv))

      // No canned stdout was registered (unparseable); register it and redo.
      ProcessController.respond(argv, { stdout: "would purge 3 sessions, 42 events\n", stderr: "", exitCode: 0 })
      clickByLabel(scopes[i])
      verify(texts().indexOf("Forget 3 sessions (42 events)? This cannot be undone.") !== -1, JSON.stringify(texts()))
      var yesArgv = argv.slice(0, argv.length - 1).concat(["--yes"])
      compare(runsMatching(yesArgv).length, 0, "--yes ran before confirm")

      clickByLabel("Forget")
      compare(runsMatching(yesArgv).length, 1)
    }
  }

  function test_forget_cancel_records_no_yes_run() {
    openMemory(readFixture(recordsGolden))
    pinClock()
    clickByLabel("Everything in this project")
    var argv = purgeRuns()[0].command
    ProcessController.respond(argv, { stdout: "would purge 1 sessions, 5 events\n", stderr: "", exitCode: 0 })
    clickByLabel("Everything in this project")
    clickByLabel("Cancel")
    var yes = purgeRuns().filter(function (r) { return r.command.indexOf("--yes") !== -1 })
    compare(yes.length, 0)
    verify(texts().indexOf("Forget 1 sessions (5 events)? This cannot be undone.") === -1)
  }

  function test_kind_word_on_each_row() {
    var golden = JSON.parse(readFixture(recordsGolden))
    golden.records.push({ id: "aaaa0009-0000-4000-8000-000000000009", ts: "2026-01-01T00:07:00Z", kind: "outcome",
      tier: "agent-declared", status: "current", text: "Swirl approved." })
    openMemory(JSON.stringify(golden))
    var t = texts()
    verify(t.indexOf("Outcome") !== -1, "no Outcome kind word: " + JSON.stringify(t))
    verify(t.indexOf("Decision") !== -1, "no Decision kind word")
    verify(t.indexOf("\uf11e") !== -1, "outcome row lacks the flag glyph")
    var bad = TestUtil.collectOutOfBounds(panel.testContentItem, panel.testContentItem,
      panel.testImplicitWidth, panel.testImplicitHeight)
    verify(bad.length === 0, bad.length + " item(s) out of bounds: " + JSON.stringify(bad).slice(0, 2000))
  }

  function test_layout_within_bounds() {
    openMemory(readFixture(recordsGolden))
    // exercise the taller states too: an open delete confirm.
    clickByTooltip("Delete")
    var bad = TestUtil.collectOutOfBounds(panel.testContentItem, panel.testContentItem,
      panel.testImplicitWidth, panel.testImplicitHeight)
    verify(bad.length === 0, bad.length + " item(s) out of bounds: " + JSON.stringify(bad).slice(0, 2000))
  }
}
