import QtQuick
import QtTest
import Quickshell
import Quickshell.Io
import "testutil.js" as TestUtil
import "../../js/scroll.js" as Scroll
import "../../js/launchers.js" as Launchers
import "../../js/format.js" as Format

// Per-agent chips, the → expansion into sub-rows, and Continue launching THAT
// agent from Launchers.LAUNCH_TABLE (task 4f4fd91c, decision 7026e48e). The
// payload is panel/testdata/this-week-here.json (day placeholders filled in);
// its first row is projects/foo (cwd /home/brian/projects/foo, the `here`
// folder, selected on open).
TestCase {
  id: testCase
  name: "PanelAgents"
  when: windowShown
  visible: true
  width: 900
  height: 900

  readonly property string fixtureUrl: Qt.resolvedUrl("../../testdata/this-week-here.json")
  readonly property real dayMs: 24 * 60 * 60 * 1000
  readonly property string fooCwd: "/home/brian/projects/foo"

  Component {
    id: panelComponent
    Loader { source: Qt.resolvedUrl("../../Panel.qml") }
  }

  property var loader: null
  property var panel: null

  TestActions { id: act; testCase: testCase }

  function readFixture() {
    var xhr = new XMLHttpRequest()
    xhr.open("GET", fixtureUrl, false)
    xhr.send()
    if (xhr.status !== 200) fail("could not read fixture: status " + xhr.status)
    return xhr.responseText
  }

  function isoDay(offsetFromToday) {
    return new Date(Date.now() - offsetFromToday * dayMs).toISOString().slice(0, 10)
  }

  function payload(mutate) {
    var text = readFixture().replace(/@NOW/g, new Date().toISOString())
    for (var n = 0; n < 7; n++) text = text.replace(new RegExp("@D" + n + "\"", "g"), isoDay(n) + "\"")
    var data = JSON.parse(text)
    if (mutate) mutate(data)
    return data
  }

  function agentEntry(id, minutesAgo) {
    return { agent: id, last_activity: new Date(Date.now() - minutesAgo * 60000).toISOString(), session_count: 1 }
  }

  // foo's agents[] set to `ids`, newest first, each 10 minutes older.
  function withAgents(ids, extra) {
    return payload(function (d) {
      var p = d.where_left_off[0].project
      p.agents = ids.map(function (id, i) { return agentEntry(id, 10 * (i + 1)) })
      p.last_agent = ids[0]
      if (extra) extra(p)
    })
  }

  function open(data) {
    ProcessController.respond(Launchers.thisWeekCommand(), { stdout: JSON.stringify(data), stderr: "", exitCode: 0 })
    ProcessController.respond(Launchers.defaultAgentCommand(), { stdout: "claude\n", stderr: "", exitCode: 0 })
    panel.open("{}")
    tryCompare(panel.view, "loading", false, 2000)
    // Rows are built from the data on the next polish; wait for the first.
    act.settled(function () { return findFoo() !== null }, "foo row never rendered")
    act.drain()
  }

  function init() {
    ProcessController.reset()
    Quickshell.reset()
    loader = panelComponent.createObject(testCase)
    tryCompare(loader, "status", Loader.Ready, 2000)
    verify(loader.item !== null, "Panel.qml failed to load: status=" + loader.status)
    panel = loader.item
    panel.testContentItem.parent = loader
  }

  function cleanup() {
    if (loader) loader.destroy()
    loader = null
    panel = null
  }

  function press(key) {
    panel.testKeyItem.forceActiveFocus()
    act.pressKey(key)
  }

  function findFoo() {
    return TestUtil.findFirst(panel.testContentItem, function (n) { return n.summary && n.stripCounts !== undefined && n.summary.display_name === "projects/foo" })
  }

  function fooRow() {
    act.settled(function () { return findFoo() !== null }, "no foo row")
    return findFoo()
  }

  function selected(prop, value) { act.settledEq(panel.view, prop, value) }

  function subRowCount(n) {
    act.settled(function () { return subRows().length === n }, "expected " + n + " sub rows, have " + subRows().length)
  }

  function namesAre(root, objectName, want) {
    act.settled(function () { return JSON.stringify(names(root, objectName)) === JSON.stringify(want) },
      objectName + " never became " + JSON.stringify(want) + "; is " + JSON.stringify(names(root, objectName)))
  }

  function names(root, objectName) {
    return TestUtil.findAll(root, function (n) { return n.objectName === objectName }).map(function (n) { return n.text })
  }

  function subRows() {
    return TestUtil.findAll(panel.testContentItem, function (n) { return n.objectName === "agentSubRow" })
  }

  function termRuns() {
    return ProcessController.runs.filter(function (r) {
      return r.command[0] === "xdg-terminal-exec" && r.command[4] !== undefined && r.command[4].indexOf("exec \"$@\"") >= 0
    })
  }

  function isTermRun(r) {
    return r.command[0] === "xdg-terminal-exec" && r.command[4] !== undefined && r.command[4].indexOf("exec \"$@\"") >= 0
  }

  function isOmarchyRun(r) { return r.command[0] === "omarchy" && r.command[1] === "agent" }

  function omarchyRuns() {
    return ProcessController.runs.filter(function (r) { return r.command[0] === "omarchy" && r.command[1] === "agent" })
  }

  // ---- chips never run under the row's action button (task 36fac381) ----
  function chipsFor(root, objectName) {
    return TestUtil.findAll(root, function (n) { return n.objectName === objectName && n.visible })
  }

  function test_chips_stop_left_of_the_recent_action_button() {
    open(withAgents(["pi", "codex", "claude"], function (p) { p.last_activity = new Date(Date.now() - 5 * 60000).toISOString() }))
    var row = fooRow()
    var button = null
    act.settled(function () {
      button = TestUtil.findFirst(row, function (n) { return n.objectName === "recentActionButton" && n.visible })
      return button !== null && chipsFor(row, "agentChip").length >= 1
    }, "recent action button / chips not visible")
    var chips = chipsFor(row, "agentChip")
    chips.forEach(function (c) {
      var right = c.mapToItem(row, c.width, 0).x
      verify(right <= button.x + 0.5, "chip right edge " + right + " is under the button at " + button.x)
    })
  }

  property var chipsComponent: Qt.createComponent(Qt.resolvedUrl("../../AgentChips.qml"))

  function test_chips_fold_into_more_when_they_do_not_fit() {
    var agents = ["pi", "codex", "claude"].map(function (id, i) { return agentEntry(id, 10 * (i + 1)) })
    var wide = createTemporaryObject(chipsComponent, testCase, { agents: agents })
    verify(wide !== null)
    compare(chipsFor(wide, "agentChip").length, 3)
    compare(chipsFor(wide, "agentChipMore").length, 0)
    var full = wide.implicitWidth
    var narrow = createTemporaryObject(chipsComponent, testCase, { agents: agents, availableWidth: full - 10 })
    var shown = chipsFor(narrow, "agentChip")
    verify(shown.length >= 1 && shown.length < 3, "shown " + shown.length)
    compare(shown[0].agentText, "Pi")
    var more = chipsFor(narrow, "agentChipMoreText")
    compare(more.length, 1)
    compare(more[0].text, "+" + (3 - shown.length))
    verify(narrow.width <= full - 10 + 0.5, "strip wider than its bound")
    var tiny = createTemporaryObject(chipsComponent, testCase, { agents: agents, availableWidth: 1 })
    compare(chipsFor(tiny, "agentChip").length, 1)
    compare(chipsFor(tiny, "agentChipMoreText")[0].text, "+2")
  }

  // ---- device chip: only for a handoff from another device ----
  function visibleDeviceChips(root, objectName) {
    return TestUtil.findAll(root, function (n) { return n.objectName === objectName && n.visible })
  }

  function test_device_chip_on_recent_row_and_here_card_only_when_non_null() {
    open(withAgents(["claude"], function (p) { p.origin_device_name = null }))
    act.drain()
    compare(visibleDeviceChips(panel.testContentItem, "deviceChip").length, 0)
    compare(visibleDeviceChips(panel.testContentItem, "hereDeviceChip").length, 0)
    cleanup()
    init()
    open(withAgents(["claude"], function (p) { p.origin_device_name = "laptop" }))
    var row = fooRow()
    act.settled(function () {
      var c = visibleDeviceChips(row, "deviceChip")
      return c.length === 1 && visibleDeviceChips(c[0], "deviceChipText").length === 1
        && visibleDeviceChips(c[0], "deviceChipText")[0].text === "from laptop"
        && visibleDeviceChips(panel.testContentItem, "hereDeviceChip").length === 1
    }, "device chips never rendered 'from laptop' on the row and the here card")
  }

  // ---- (1) chips ----
  // An old last_activity keeps the labelled action button off the row, so the
  // chips have the room the MAX_AGENT_CHIPS cap alone decides.
  function stale(p) { p.last_activity = new Date(Date.now() - 300 * 60000).toISOString() }

  // The strip is built standalone with no availableWidth bound: whether the
  // panel row has room for three chips depends on the machine's default font
  // (Liberation Sans folds the third chip into +1), so the row's width must
  // not decide what this test asserts. Folding itself is covered by
  // test_chips_fold_into_more_when_they_do_not_fit.
  function test_chips_render_newest_first_with_display_names() {
    var strip = createTemporaryObject(chipsComponent, testCase, { agents: ["hermes", "pi", "claude"].map(function (id, i) { return agentEntry(id, 10 * (i + 1)) }) })
    verify(strip !== null)
    compare(JSON.stringify(names(strip, "agentChipName")), JSON.stringify(["Hermes", "Pi", "Claude Code"]))
    compare(chipsFor(strip, "agentChipMoreText").length, 0)
  }

  function test_more_chip_when_agents_exceed_the_cap() {
    var ids = ["hermes", "pi", "claude", "codex", "opencode"]
    compare(Format.MAX_AGENT_CHIPS, 3)
    var strip = createTemporaryObject(chipsComponent, testCase, { agents: ids.map(function (id, i) { return agentEntry(id, 10 * (i + 1)) }) })
    compare(JSON.stringify(names(strip, "agentChipName")), JSON.stringify(["Hermes", "Pi", "Claude Code"]))
    compare(JSON.stringify(names(strip, "agentChipMoreText")), JSON.stringify(["+2"]))
  }

  function test_here_card_shows_chips_too() {
    open(withAgents(["hermes", "pi", "claude"]))
    var here = null
    act.settled(function () {
      here = TestUtil.findFirst(panel.testContentItem, function (n) { return n.objectName === "hereAgentChips" })
      return here !== null
    }, "hereAgentChips never rendered")
    namesAre(here, "agentChipName", ["Hermes", "Pi", "Claude Code"])
  }

  // ---- (2) expansion and sub-row launch ----
  function test_right_expands_three_sub_rows_and_enter_on_pi_launches_pi_in_cwd() {
    open(withAgents(["hermes", "pi", "claude"]))
    compare(subRows().length, 0)
    press(Qt.Key_Right)
    subRowCount(3)
    namesAre(panel.testContentItem, "agentSubRowName", ["Hermes", "Pi", "Claude Code"])

    selected("selectedIndex", 0)
    press(Qt.Key_J)
    selected("selectedIndex", 1)
    selected("selectedAgent", "hermes")
    press(Qt.Key_J)
    selected("selectedIndex", 2)
    selected("selectedAgent", "pi")
    press(Qt.Key_J)
    selected("selectedAgent", "claude")
    press(Qt.Key_K)
    selected("selectedAgent", "pi")

    press(Qt.Key_Return)
    var runs = act.launchedCount(isTermRun, 1, "Enter on the pi sub-row")
    compare(runs[0].workingDirectory, fooCwd)
    compare(runs[0].command[6], fooCwd)
    compare(JSON.stringify(runs[0].command.slice(7, 8)), JSON.stringify(Launchers.LAUNCH_TABLE.pi.argv))
    verify(runs[0].command[8].indexOf("handoff-0042") >= 0, runs[0].command[8])
    compare(omarchyRuns().length, 0)
  }

  function test_left_collapses_and_returns_selection_to_the_folder_row() {
    open(withAgents(["hermes", "pi", "claude"]))
    press(Qt.Key_Right)
    subRowCount(3)
    press(Qt.Key_J)
    press(Qt.Key_J)
    selected("selectedAgent", "pi")
    press(Qt.Key_Left)
    subRowCount(0)
    selected("selectedIndex", 0)
    selected("selectedAgent", "")
  }

  function test_click_on_chip_strip_expands_and_sub_row_continue_launches_that_agent() {
    open(withAgents(["pi", "hermes", "claude"]))
    function findStrip() { return TestUtil.findFirst(fooRow(), function (n) { return n.objectName === "agentChips" }) }
    act.clickWhenReady(findStrip, { x: 2, y: 4, desc: "foo's agent chip strip", until: function () { return subRows().length === 3 } })
    function continueButtons() {
      return TestUtil.findAll(panel.testContentItem, function (n) { return n.objectName === "agentSubRowContinue" })
    }
    act.settled(function () { return continueButtons().length === 3 }, "expected 3 sub-row Continue buttons")
    act.clickWhenReady(function () {
      var b = continueButtons()
      if (b.length !== 3) return null
      Scroll.reveal(panel.view.scrollArea, b[1])
      return b[1]
    }, { desc: "second sub-row Continue" })
    var runs = act.launchedCount(isTermRun, 1, "sub-row Continue")
    compare(JSON.stringify(runs[0].command.slice(7, 9)), JSON.stringify(Launchers.LAUNCH_TABLE.hermes.argv.slice(0, 2)))
  }

  function test_expanded_sub_row_launch_ignores_an_open_window() {
    open(withAgents(["hermes", "pi", "claude"], function (p) { p.window = "0x55aa" }))
    press(Qt.Key_Right)
    subRowCount(3)
    press(Qt.Key_J)
    press(Qt.Key_J)
    press(Qt.Key_Return)
    act.launchedCount(isTermRun, 1, "sub-row Enter with an open window")
    act.drain()
    compare(termRuns().length, 1)
    compare(ProcessController.runs.filter(function (r) { return r.command[0] === "hyprctl" }).length, 0)
  }

  // ---- (3) folder-row Enter ----
  function test_enter_on_collapsed_row_launches_newest_agent_with_handoff_query() {
    open(withAgents(["hermes", "pi", "claude"]))
    press(Qt.Key_Return)
    var runs = act.launchedCount(isTermRun, 1, "Enter on the collapsed row")
    var argv = runs[0].command.slice(7)
    compare(JSON.stringify(argv.slice(0, 2)), JSON.stringify(["hermes", "chat"]))
    var q = argv[argv.length - 1]
    verify(q.indexOf("--query=") === 0, q)
    verify(q.indexOf("handoff-0042") >= 0 && q.indexOf("Wire the bar") >= 0, q)
    compare(runs[0].workingDirectory, fooCwd)
  }

  function test_agent_outside_the_table_falls_back_to_omarchy_agent_prompt() {
    open(withAgents(["opencode"]))
    press(Qt.Key_Return)
    var a = act.launchedCount(isOmarchyRun, 1, "omarchy agent prompt fallback")
    compare(termRuns().length, 0)
    compare(JSON.stringify(a[0].command.slice(0, 3)), JSON.stringify(["omarchy", "agent", "prompt"]))
    verify(a[0].command[3].indexOf("handoff-0042") >= 0)
    compare(a[0].workingDirectory, fooCwd)
  }

  // ---- (4) the handoff line names its writer ----
  function test_handoff_line_names_its_writer() {
    open(withAgents(["hermes", "pi"], function (p) { p.handoff_agent = "pi" }))
    var texts = []
    var next = []
    act.settled(function () {
      texts = TestUtil.collectVisibleTexts(panel.testContentItem)
      next = texts.filter(function (t) { return t.indexOf("Next (Pi):") === 0 })
      return next.length >= 1
    }, "no 'Next (Pi):' line; " + JSON.stringify(texts))
    compare(next[0], "Next (Pi): Wire the bar")
    texts.forEach(function (t) { verify(t.indexOf("Continue opens") < 0, t) })
  }

  function test_handoff_line_is_plain_next_without_a_writer() {
    open(withAgents(["hermes"]))
    act.settled(function () { return TestUtil.collectVisibleTexts(panel.testContentItem).indexOf("Next: Wire the bar") >= 0 }, "plain Next line never rendered")
  }
}
