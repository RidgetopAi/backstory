import QtQuick
import QtTest
import Quickshell
import Quickshell.Io
import "testutil.js" as TestUtil
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
    tryCompare(panel, "loading", false, 2000)
    wait(20)
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
    keyClick(key)
  }

  function fooRow() {
    var row = TestUtil.findFirst(panel.testContentItem, function (n) { return n.summary && n.stripCounts !== undefined && n.summary.display_name === "projects/foo" })
    verify(row !== null, "no foo row")
    return row
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

  function omarchyRuns() {
    return ProcessController.runs.filter(function (r) { return r.command[0] === "omarchy" && r.command[1] === "agent" })
  }

  // ---- (1) chips ----
  function test_chips_render_newest_first_with_display_names() {
    open(withAgents(["hermes", "pi", "claude"]))
    compare(JSON.stringify(names(fooRow(), "agentChipName")), JSON.stringify(["Hermes", "Pi", "Claude Code"]))
    var more = TestUtil.findAll(fooRow(), function (n) { return n.objectName === "agentChipMoreText" })
    compare(more.length, 0)
  }

  function test_more_chip_when_agents_exceed_the_cap() {
    var ids = ["hermes", "pi", "claude", "codex", "opencode"]
    compare(Format.MAX_AGENT_CHIPS, 3)
    open(withAgents(ids))
    compare(JSON.stringify(names(fooRow(), "agentChipName")), JSON.stringify(["Hermes", "Pi", "Claude Code"]))
    compare(JSON.stringify(names(fooRow(), "agentChipMoreText")), JSON.stringify(["+2"]))
  }

  function test_here_card_shows_chips_too() {
    open(withAgents(["hermes", "pi", "claude"]))
    var here = TestUtil.findFirst(panel.testContentItem, function (n) { return n.objectName === "hereAgentChips" })
    verify(here !== null)
    compare(JSON.stringify(names(here, "agentChipName")), JSON.stringify(["Hermes", "Pi", "Claude Code"]))
  }

  // ---- (2) expansion and sub-row launch ----
  function test_right_expands_three_sub_rows_and_enter_on_pi_launches_pi_in_cwd() {
    open(withAgents(["hermes", "pi", "claude"]))
    compare(subRows().length, 0)
    press(Qt.Key_Right)
    compare(subRows().length, 3)
    compare(JSON.stringify(names(panel.testContentItem, "agentSubRowName")), JSON.stringify(["Hermes", "Pi", "Claude Code"]))

    compare(panel.selectedIndex, 0)
    press(Qt.Key_J)
    compare(panel.selectedIndex, 1)
    compare(panel.selectedAgent, "hermes")
    press(Qt.Key_J)
    compare(panel.selectedIndex, 2)
    compare(panel.selectedAgent, "pi")
    press(Qt.Key_J)
    compare(panel.selectedAgent, "claude")
    press(Qt.Key_K)
    compare(panel.selectedAgent, "pi")

    press(Qt.Key_Return)
    var runs = termRuns()
    compare(runs.length, 1)
    compare(runs[0].workingDirectory, fooCwd)
    compare(runs[0].command[6], fooCwd)
    compare(JSON.stringify(runs[0].command.slice(7, 8)), JSON.stringify(Launchers.LAUNCH_TABLE.pi.argv))
    verify(runs[0].command[8].indexOf("handoff-0042") >= 0, runs[0].command[8])
    compare(omarchyRuns().length, 0)
  }

  function test_left_collapses_and_returns_selection_to_the_folder_row() {
    open(withAgents(["hermes", "pi", "claude"]))
    press(Qt.Key_Right)
    press(Qt.Key_J)
    press(Qt.Key_J)
    compare(panel.selectedAgent, "pi")
    press(Qt.Key_Left)
    compare(subRows().length, 0)
    compare(panel.selectedIndex, 0)
    compare(panel.selectedAgent, "")
  }

  function test_click_on_chip_strip_expands_and_sub_row_continue_launches_that_agent() {
    open(withAgents(["pi", "hermes", "claude"]))
    var strip = TestUtil.findFirst(fooRow(), function (n) { return n.objectName === "agentChips" })
    mouseClick(strip, 2, strip.height / 2)
    compare(subRows().length, 3)
    wait(50)
    var btns = TestUtil.findAll(panel.testContentItem, function (n) { return n.objectName === "agentSubRowContinue" })
    compare(btns.length, 3)
    panel.revealInScroller(btns[1])
    mouseClick(btns[1], btns[1].width / 2, btns[1].height / 2)
    var runs = termRuns()
    compare(runs.length, 1)
    compare(JSON.stringify(runs[0].command.slice(7, 9)), JSON.stringify(Launchers.LAUNCH_TABLE.hermes.argv.slice(0, 2)))
  }

  function test_expanded_sub_row_launch_ignores_an_open_window() {
    open(withAgents(["hermes", "pi", "claude"], function (p) { p.window = "0x55aa" }))
    press(Qt.Key_Right)
    press(Qt.Key_J)
    press(Qt.Key_J)
    press(Qt.Key_Return)
    compare(termRuns().length, 1)
    compare(ProcessController.runs.filter(function (r) { return r.command[0] === "hyprctl" }).length, 0)
  }

  // ---- (3) folder-row Enter ----
  function test_enter_on_collapsed_row_launches_newest_agent_with_handoff_query() {
    open(withAgents(["hermes", "pi", "claude"]))
    press(Qt.Key_Return)
    var runs = termRuns()
    compare(runs.length, 1)
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
    compare(termRuns().length, 0)
    var a = omarchyRuns()
    compare(a.length, 1)
    compare(JSON.stringify(a[0].command.slice(0, 3)), JSON.stringify(["omarchy", "agent", "prompt"]))
    verify(a[0].command[3].indexOf("handoff-0042") >= 0)
    compare(a[0].workingDirectory, fooCwd)
  }

  // ---- (4) the handoff line names its writer ----
  function test_handoff_line_names_its_writer() {
    open(withAgents(["hermes", "pi"], function (p) { p.handoff_agent = "pi" }))
    var texts = TestUtil.collectVisibleTexts(panel.testContentItem)
    var next = texts.filter(function (t) { return t.indexOf("Next (Pi):") === 0 })
    verify(next.length >= 1, JSON.stringify(texts))
    compare(next[0], "Next (Pi): Wire the bar")
    texts.forEach(function (t) { verify(t.indexOf("Continue opens") < 0, t) })
  }

  function test_handoff_line_is_plain_next_without_a_writer() {
    open(withAgents(["hermes"]))
    var texts = TestUtil.collectVisibleTexts(panel.testContentItem)
    verify(texts.indexOf("Next: Wire the bar") >= 0)
  }
}
