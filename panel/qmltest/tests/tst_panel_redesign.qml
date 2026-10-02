import QtQuick
import QtTest
import Quickshell
import Quickshell.Io
import "testutil.js" as TestUtil
import "../../js/launchers.js" as Launchers

// The redesigned panel (task fc1f340d): HERE card, Continue
// (focus-or-launch, naming the agent), 7-day strips, summary line,
// keyboard, and the layout/warning gates with each view open. The payload
// is panel/testdata/this-week-here.json with its day placeholders filled
// in relative to now.
TestCase {
  id: testCase
  name: "PanelRedesign"
  when: windowShown
  visible: true
  width: 900
  height: 900

  readonly property string fixtureUrl: Qt.resolvedUrl("../../testdata/this-week-here.json")
  readonly property real dayMs: 24 * 60 * 60 * 1000

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

  // D<n> is n days ago (UTC), @NOW the current instant.
  function payload(mutate) {
    var text = readFixture().replace(/@NOW/g, new Date().toISOString())
    for (var n = 0; n < 7; n++) text = text.replace(new RegExp("@D" + n + "\"", "g"), isoDay(n) + "\"")
    var data = JSON.parse(text)
    if (mutate) mutate(data)
    return data
  }

  function open(data, defaultAgent) {
    ProcessController.respond(Launchers.thisWeekCommand(), { stdout: JSON.stringify(data), stderr: "", exitCode: 0 })
    ProcessController.respond(Launchers.defaultAgentCommand(), { stdout: defaultAgent === undefined ? "claude\n" : defaultAgent, stderr: "", exitCode: 0 })
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

  function texts() { return TestUtil.collectVisibleTexts(panel.testContentItem) }

  function hasText(s) { return texts().indexOf(s) >= 0 }

  function textContaining(s) {
    var all = texts()
    for (var i = 0; i < all.length; i++) if (all[i].indexOf(s) >= 0) return all[i]
    return ""
  }

  function clickText(s) {
    var node = TestUtil.findFirst(panel.testContentItem, function (n) { return n.text === s })
    verify(node !== null, "no visible text " + JSON.stringify(s) + " in " + JSON.stringify(texts()))
    mouseClick(node, node.width / 2, node.height / 2)
  }

  function runs(prefix) {
    return ProcessController.runs.filter(function (r) { return r.command[0] === prefix[0] && r.command[1] === prefix[1] })
  }

  function agentRuns() { return runs(["omarchy", "agent"]) }
  function hyprRuns() { return runs(["hyprctl", "dispatch"]) }

  // ---- (1) the here card ----
  function test_here_card_shows_project_chip_next_and_buttons() {
    open(payload())
    var tree = texts()
    compare(tree[0], "Backstory")
    verify(hasText("foo"), "card title 'foo' missing: " + JSON.stringify(tree))
    verify(hasText("here"), "'here' chip missing")
    verify(hasText("Next: Wire the bar"))
    verify(hasText("Continue in Claude Code"))
    verify(hasText("Terminal"))
    verify(hasText("Memory"))
    verify(textContaining("sessions this week").indexOf("19 sessions this week") >= 0, "facts line: " + textContaining("sessions this week"))
    for (var i = 0; i < tree.length; i++) verify(tree[i].toUpperCase().indexOf("THE WEEK") < 0, "found 'THE WEEK': " + tree[i])
  }

  function test_no_here_means_no_card() {
    open(payload(function (d) { delete d.here }))
    verify(!hasText("here"))
    verify(!hasText("Terminal"))
  }

  // ---- (2) Continue ----
  function test_continue_with_window_focuses_and_does_not_launch() {
    open(payload(function (d) { d.where_left_off[0].project.window = "0x55aa" }))
    clickText("Continue in Claude Code")
    compare(agentRuns().length, 0)
    var h = hyprRuns()
    compare(h.length, 1)
    compare(JSON.stringify(h[0].command), JSON.stringify(Launchers.focusWindowCommand("0x55aa")))
    compare(h[0].command[2], 'hl.dsp.focus({ window = "address:0x55aa" })')
  }

  function test_continue_without_window_launches_agent_in_cwd() {
    open(payload())
    clickText("Continue in Claude Code")
    compare(hyprRuns().length, 0)
    var a = agentRuns()
    compare(a.length, 1)
    compare(a[0].command.length, 4)
    compare(JSON.stringify(a[0].command.slice(0, 3)), JSON.stringify(["omarchy", "agent", "prompt"]))
    verify(a[0].command[3].indexOf("handoff-0042") >= 0, a[0].command[3])
    verify(a[0].command[3].indexOf("Wire the bar") >= 0, a[0].command[3])
    compare(a[0].workingDirectory, "/home/brian/projects/foo")
  }

  function test_continue_with_hostile_window_launches_agent_never_hyprctl() {
    open(payload(function (d) { d.where_left_off[0].project.window = "0x1; rm" }))
    clickText("Continue in Claude Code")
    compare(hyprRuns().length, 0)
    compare(agentRuns().length, 1)
  }

  function test_focus_falls_back_to_focuswindow_when_dispatch_fails() {
    open(payload(function (d) { d.where_left_off[0].project.window = "0x55aa" }))
    ProcessController.respond(Launchers.focusWindowCommand("0x55aa"), { stdout: "", stderr: "", exitCode: 1 })
    clickText("Continue in Claude Code")
    var h = hyprRuns()
    compare(h.length, 2)
    compare(JSON.stringify(h[1].command), JSON.stringify(["hyprctl", "dispatch", "focuswindow", "address:0x55aa"]))
  }

  // ---- (3) the agent notice and the unset default ----
  function test_agent_notice_when_last_agent_differs_from_default() {
    open(payload(function (d) { d.where_left_off[0].project.last_agent = "codex" }))
    var line = textContaining("Continue opens")
    verify(line.indexOf("Codex") >= 0 && line.indexOf("Claude Code") >= 0, "notice: " + line)
  }

  function test_no_notice_when_same_agent() {
    open(payload())
    compare(textContaining("Continue opens"), "")
  }

  function test_no_notice_when_last_agent_empty() {
    open(payload(function (d) { d.where_left_off[0].project.last_agent = "" }))
    compare(textContaining("Continue opens"), "")
  }

  function test_unset_default_offers_picker() {
    open(payload(), "")
    verify(hasText("Choose default agent"))
    compare(textContaining("Continue opens"), "")
    clickText("Choose default agent")
    compare(agentRuns().length, 1)
    compare(JSON.stringify(agentRuns()[0].command), JSON.stringify(["omarchy", "agent", "--pick"]))
  }

  // ---- (4) Recent rows ----
  function strips(rowName) {
    var row = TestUtil.findFirst(panel.testContentItem, function (n) { return n.summary && n.stripCounts !== undefined && n.summary.display_name === rowName })
    verify(row !== null, "no row " + rowName)
    return TestUtil.findAll(row, function (n) { return n.objectName === "stripCell" })
  }

  function test_strip_shades_four_of_seven_days_strongest_for_busiest() {
    open(payload())
    var cells = strips("projects/foo")
    compare(cells.length, 7)
    var shaded = cells.filter(function (c) { return c.shaded })
    compare(shaded.length, 4)
    var strongest = cells.reduce(function (a, c) { return c.strength > a.strength ? c : a }, cells[0])
    compare(strongest.modelData, 13)
    // D6 is the oldest of the seven cells, so the 13-session day is cell 0.
    compare(cells[0].modelData, 13)
    compare(cells[0].strength, 1)
    verify(cells[6].strength < 1 && cells[6].shaded)
  }

  function test_summary_line_equals_week_sums() {
    var data = payload()
    open(data)
    var keys = {}, s = 0, f = 0, n = 0
    data.week.forEach(function (d) { keys[d.project_key] = true; s += d.sessions; f += d.files_touched; n += d.records_written })
    compare(textContaining("This week:"), "This week: " + Object.keys(keys).length + " projects · " + s + " sessions · " + f + " files · " + n + " notes")
  }

  function test_workspace_row_reads_workspace_notes() {
    open(payload())
    var row = TestUtil.findFirst(panel.testContentItem, function (n) { return n.objectName === "rowName" && n.text.indexOf("notes") >= 0 })
    verify(row !== null)
    verify(/workspace notes$/.test(row.text), row.text)
    compare(row.text, "notes \u00b7 workspace notes")
  }

  // Decision 63ce9687 P4: no row, Recent or here, shows the "projects/" prefix.
  function test_recent_rows_and_here_card_drop_projects_prefix() {
    open(payload(function (d) {
      d.where_left_off[1].project.display_name = "projects/bar"
    }))
    var rows = TestUtil.findAll(panel.testContentItem, function (n) { return n.objectName === "rowName" })
    var names = rows.map(function (r) { return r.text })
    verify(names.indexOf("foo") >= 0, JSON.stringify(names))
    verify(names.indexOf("bar") >= 0, JSON.stringify(names))
    names.forEach(function (n) { verify(n.indexOf("projects/") < 0, "Recent row shows prefix: " + n) })
    var tree = texts()
    tree.forEach(function (t) { verify(t.indexOf("projects/") < 0, "visible text shows prefix: " + t) })
    var title = TestUtil.findFirst(panel.testContentItem, function (n) { return n.objectName === "hereTitle" })
    verify(title !== null, "here card title missing")
    compare(title.text, "foo")
  }

  // ---- (5) keyboard ----
  function press(key) {
    panel.testKeyItem.forceActiveFocus()
    keyClick(key)
  }

  function test_keys_select_continue_terminal_memory_and_close() {
    open(payload())
    compare(panel.selectedIndex, 0)
    press(Qt.Key_J)
    compare(panel.selectedIndex, 1)
    // row 2 is bar (cwd /home/brian/bar), codex, no handoff
    press(Qt.Key_Return)
    var a = agentRuns()
    compare(a.length, 1)
    compare(a[0].workingDirectory, "/home/brian/bar")
    verify(a[0].command[3].indexOf("bar") >= 0)

    press(Qt.Key_T)
    var last = Quickshell.lastCall()
    compare(JSON.stringify(last.command), JSON.stringify(Launchers.terminalLaunchCommand("/home/brian/bar")))
    compare(last.workingDirectory, "/home/brian/bar")

    press(Qt.Key_M)
    compare(panel.memoryOpen, true)
    compare(panel.memoryProjectKey, "bar-key")
    compare(panel.memoryLocation, "/home/brian/bar")

    press(Qt.Key_Escape)
    compare(panel.opened, false)
    compare(panel.memoryOpen, false)
  }

  function test_k_and_arrows_move_and_clamp() {
    open(payload())
    press(Qt.Key_Down)
    press(Qt.Key_Down)
    press(Qt.Key_Down)
    compare(panel.selectedIndex, 2)
    press(Qt.Key_K)
    compare(panel.selectedIndex, 1)
    press(Qt.Key_Up)
    press(Qt.Key_Up)
    compare(panel.selectedIndex, 0)
  }

  function test_r_refreshes() {
    open(payload())
    var before = runs(["backstory", "this-week"]).length
    press(Qt.Key_R)
    compare(runs(["backstory", "this-week"]).length, before + 1)
  }

  function test_row_keys_inert_while_memory_view_open() {
    open(payload())
    panel.openMemory("foo-key", "projects/foo", "/home/brian/projects/foo")
    var before = Quickshell.execDetachedCalls.length
    press(Qt.Key_T)
    compare(Quickshell.execDetachedCalls.length, before)
    press(Qt.Key_Escape)
    compare(panel.opened, false)
  }

  function test_footer_lists_the_keys() {
    open(payload())
    var f = textContaining("Esc")
    verify(f.indexOf("continue") >= 0 && f.indexOf("terminal") >= 0 && f.indexOf("memory") >= 0, f)
  }

  // ---- (7) layout gates ----
  function assertWithinBounds(label) {
    var bad = TestUtil.collectOutOfBounds(panel.testContentItem, panel.testContentItem, panel.testImplicitWidth, panel.testImplicitHeight)
    verify(bad.length === 0, label + ": " + JSON.stringify(bad).slice(0, 1500))
  }

  function test_layout_panel_open_with_here() {
    open(payload())
    assertWithinBounds("panel open")
  }

  function test_layout_unset_default_and_notice() {
    open(payload(function (d) { d.where_left_off[0].project.last_agent = "opencode" }), "")
    assertWithinBounds("unset default")
    cleanup(); init()
    open(payload(function (d) { d.where_left_off[0].project.last_agent = "hermes" }), "claude\n")
    assertWithinBounds("notice")
  }

  function test_layout_memory_open() {
    open(payload())
    panel.openMemory("foo-key", "projects/foo", "/home/brian/projects/foo")
    wait(20)
    assertWithinBounds("memory open")
  }

  function test_layout_group_editor_open() {
    open(payload())
    ProcessController.respond(Launchers.groupListCommand(), { stdout: JSON.stringify({ groups: [], ungrouped: [] }), stderr: "", exitCode: 0 })
    panel.groupEditorOpen = true
    wait(20)
    assertWithinBounds("group editor open")
  }
}
