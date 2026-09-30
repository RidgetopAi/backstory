import QtQuick
import QtTest
import Quickshell
import Quickshell.Io
import "testutil.js" as TestUtil
import "../../js/launchers.js" as Launchers
import "../../js/memory.js" as MemoryJs

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
    compare(runsMatching(Launchers.recordsCommand(projectKey, cwd, true)).length, 1)
    verify(texts().indexOf("deleted") !== -1, "deleted status mark missing under --history")
  }

  function test_empty_state() {
    openMemory(JSON.stringify({ project_key: projectKey, records: [] }))
    verify(texts().indexOf("Nothing saved for acme yet.") !== -1, JSON.stringify(texts()))
  }

  function test_records_error_shows_stderr() {
    ProcessController.respond(Launchers.recordsCommand(projectKey, cwd, false), { stdout: "", stderr: "store is locked", exitCode: 1 })
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
    verify(runsMatching(Launchers.recordsCommand(projectKey, cwd, false)).length >= 2)
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
    var scopes = ["Last hour", "Today", "Everything here"]
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
      compare(argv[4], "--location")
      compare(argv[5], cwd)
      compare(argv.indexOf("--yes"), -1)
      if (scopes[i] === "Everything here") compare(argv.indexOf("--since"), -1)
      else if (scopes[i] === "Last hour") compare(argv[argv.indexOf("--since") + 1], "2026-03-10T14:30:45Z")
      else verify(/^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ$/.test(argv[argv.indexOf("--since") + 1]), JSON.stringify(argv))

      // No canned stdout was registered (unparseable); register it and redo.
      ProcessController.respond(argv, { stdout: "would purge 3 sessions, 42 events, 5 records\n", stderr: "", exitCode: 0 })
      clickByLabel(scopes[i])
      verify(texts().indexOf("Forget 3 sessions (42 events) and 5 saved records from acme? This cannot be undone.") !== -1, JSON.stringify(texts()))
      var yesArgv = argv.slice(0, argv.length - 1).concat(["--yes"])
      compare(runsMatching(yesArgv).length, 0, "--yes ran before confirm")

      clickByLabel("Forget")
      compare(runsMatching(yesArgv).length, 1)
    }
  }

  function test_forget_cancel_records_no_yes_run() {
    openMemory(readFixture(recordsGolden))
    pinClock()
    clickByLabel("Everything here")
    var argv = purgeRuns()[0].command
    ProcessController.respond(argv, { stdout: "would purge 1 sessions, 5 events, 0 records\n", stderr: "", exitCode: 0 })
    clickByLabel("Everything here")
    clickByLabel("Cancel")
    var yes = purgeRuns().filter(function (r) { return r.command.indexOf("--yes") !== -1 })
    compare(yes.length, 0)
    verify(texts().indexOf("Forget 1 sessions (5 events) and 0 saved records from acme? This cannot be undone.") === -1)
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
    tryCompare(panel, "loading", false, 2000)
    wait(20)
    clickByTooltip("Memory")
    verify(panel.memoryOpen)
    pinClock()
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
      var runs = purgeRuns()
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
    verify(texts().indexOf("Nothing to forget for last hour.") !== -1, JSON.stringify(texts()))
    verify(texts().join("|").indexOf("This cannot be undone") === -1, "a confirm showed for 0/0/0")

    clickByLabel("Today")
    var want = "Forget 1 sessions (27 events) and 2 saved records from projects/backstory-feedback? This cannot be undone."
    verify(texts().indexOf(want) !== -1, JSON.stringify(texts()))
    verify(texts().indexOf("Nothing to forget for last hour.") === -1, "stale note left behind")
    var yesArgv = Launchers.purgeCommand(wsKey, labelCwd, todaySince, false)
    compare(runsMatching(yesArgv).length, 0, "--yes ran before confirm")
    clickByLabel("Forget")
    compare(runsMatching(yesArgv).length, 1)
  }

  function test_records_only_window_is_not_nothing_to_forget() {
    openLabelRowMemory(readFixture(recordsGolden))
    var since = MemoryJs.FORGET_SCOPES[0].since(pinnedNow)
    ProcessController.respond(Launchers.purgeCommand(wsKey, labelCwd, since, true), { stdout: "would purge 0 sessions, 0 events, 2 records\n", stderr: "", exitCode: 0 })
    clickByLabel("Last hour")
    verify(texts().indexOf("Forget 0 sessions (0 events) and 2 saved records from projects/backstory-feedback? This cannot be undone.") !== -1, JSON.stringify(texts()))
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
    compare(f.contentY, 0)
  }

  function clickForgetOptionScrolledToTop(label) {
    scrollToTop()
    var button = TestUtil.findFirst(panel.testContentItem, function (n) { return n.label === label })
    verify(button !== null, "no " + label + " button")
    // Scrolled to the top the option is below the viewport, so the mouse
    // cannot reach it: trigger the click handler itself.
    button.clicked()
  }

  function findTextStarting(prefix) {
    return TestUtil.findFirst(panel.testContentItem, function (n) {
      return typeof n.text === "string" && n.text.indexOf(prefix) === 0
    })
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
    clickForgetOptionScrolledToTop("Last hour")
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
    tryVerify(function () { return findTextStarting("Forget this record?") !== null }, 2000)
    var confirm = findTextStarting("Forget this record?")
    tryVerify(function () {
      var top = confirm.mapToItem(flickable(), 0, 0).y
      return top >= -0.5 && top + confirm.height <= flickable().height + 0.5
    }, 2000, "delete confirm never scrolled into view")
    assertInViewport(confirm, "the delete confirm")
  }
}
