import QtQuick
import QtTest
import Quickshell
import Quickshell.Io
import qs.Commons
import "testutil.js" as TestUtil
import "../../js/launchers.js" as Launchers
import "../../js/format.js" as Format

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
    tryCompare(panel.view, "loading", false, 2000)
    act.quiescent(function () { return [texts(), panel.testImplicitHeight] }, "panel never finished rendering")
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
    Style.testFontScale = 1
    Style.testSpacingScaleWithFont = true
  }

  function texts() { return TestUtil.collectVisibleTexts(panel.testContentItem) }

  function hasText(s) { return texts().indexOf(s) >= 0 }

  function textContaining(s) {
    var all = texts()
    for (var i = 0; i < all.length; i++) if (all[i].indexOf(s) >= 0) return all[i]
    return ""
  }

  function clickText(s) {
    act.clickWhenReady(function () {
      return TestUtil.findFirst(panel.testContentItem, function (n) { return n.text === s })
    }, { desc: "visible text " + JSON.stringify(s) })
  }

  function runs(prefix) {
    return ProcessController.runs.filter(function (r) { return r.command[0] === prefix[0] && r.command[1] === prefix[1] })
  }

  function isRun(prefix) { return function (r) { return r.command[0] === prefix[0] && r.command[1] === prefix[1] } }
  function isTermRun(r) { return isRun(["xdg-terminal-exec", "--"])(r) && r.command[2] === "sh" && r.command[4] !== undefined && r.command[4].indexOf("exec \"$@\"") >= 0 }
  function isHyprRun(r) { return isRun(["hyprctl", "dispatch"])(r) }
  function isAgentRun(r) { return isRun(["omarchy", "agent"])(r) }

  function agentRuns() { return runs(["omarchy", "agent"]) }
  function termRuns() { return runs(["xdg-terminal-exec", "--"]).filter(function (r) { return r.command[2] === "sh" && r.command[4] !== undefined && r.command[4].indexOf("exec \"$@\"") >= 0 }) }
  function hyprRuns() { return runs(["hyprctl", "dispatch"]) }

  // ---- (1) the here card ----
  function test_here_card_shows_project_chip_next_and_buttons() {
    open(payload())
    var tree = texts()
    compare(tree.slice(0, 2), [panel.view.scrollGlyph, "Backstory"])
    verify(hasText("foo"), "card title 'foo' missing: " + JSON.stringify(tree))
    verify(hasText("here"), "'here' chip missing")
    verify(hasText("Next: Wire the bar"))
    verify(hasText("Continue in Claude Code"))
    verify(hasText("Terminal"))
    verify(hasText("Memory"))
    verify(textContaining("sessions this week").indexOf("19 sessions this week") >= 0, "facts line: " + textContaining("sessions this week"))
    for (var i = 0; i < tree.length; i++) verify(tree[i].toUpperCase().indexOf("THE WEEK") < 0, "found 'THE WEEK': " + tree[i])
  }

  // The card's buttons span its inner width: the last ends as far from the
  // card's right edge as the first starts from its left and the row sits
  // from its bottom, whether the Continue label is long or short.
  function test_here_card_buttons_pad_like_the_card() {
    open(payload())
    assertHereCardGaps("Continue in Claude Code")
    named("hereCard").continueLabel = "Go"
    assertHereCardGaps("Go")
  }

  function named(name) { return TestUtil.findFirst(panel.testContentItem, function (n) { return n.objectName === name }) }

  function assertHereCardGaps(desc) {
    var card = named("hereCard")
    act.quiescent(function () { return [named("hereMemory").x, named("hereContinue").width] }, desc + ": never settled")
    var first = named("hereContinue").mapToItem(card, 0, 0)
    var lastButton = named("hereMemory")
    var last = lastButton.mapToItem(card, 0, 0)
    var left = first.x
    fuzzyCompare(card.width - (last.x + lastButton.width), left, 0.5, desc + ": right gap vs left gap")
    fuzzyCompare(card.height - (last.y + lastButton.height), left, 0.5, desc + ": bottom gap vs left gap")
  }

  // Text sizes a theme can set (`[font] base-size`, 12 by default), and 16
  // with spacing that does not follow it (`[spacing] scale-with-font = false`).
  function test_layout_at_text_sizes_data() {
    return [
      { tag: "base-size 10", fontScale: 10 / 12, withFont: true },
      { tag: "base-size 16", fontScale: 16 / 12, withFont: true },
      { tag: "base-size 20", fontScale: 20 / 12, withFont: true },
      { tag: "base-size 16, fixed spacing", fontScale: 16 / 12, withFont: false }
    ]
  }

  // Nothing is cut off by the scroller (Dismiss included), Dismiss ends
  // exactly at its row's right padding whatever width the warning glyph
  // draws at (a font without it draws a narrow box), the HERE card's
  // buttons pad like the card, and Continue's label lies inside its button
  // (shrunk or elided as qs.Ui Button does when there is too little room).
  function test_layout_at_text_sizes(row) {
    cleanup()
    Style.testFontScale = row.fontScale
    Style.testSpacingScaleWithFont = row.withFont
    init()
    open(payload(function (d) {
      d.attention.push({ kind: "possibly-stale-handoff", project_key: "foo", reason: "foo: handoff possibly stale — cargo test failed (exit 101) after it", handoff_id: "h-foo", evidence_ids: ["7"] })
    }))
    act.quiescent(function () { return TestUtil.collectClipped(panel.testContentItem) }, row.tag + ": never settled")
    compare(JSON.stringify(TestUtil.collectClipped(panel.testContentItem)), "[]", row.tag + ": clipped by the scroller")
    var dismiss = named("attentionDismiss")
    var dismissRow = dismiss.parent
    verify(Math.abs(dismiss.x + dismiss.width - (dismissRow.width - dismissRow.rightPadding)) < 0.5,
      row.tag + ": Dismiss ends at " + (dismiss.x + dismiss.width) + ", want the row's width minus its right padding ("
      + (dismissRow.width - dismissRow.rightPadding) + ")")
    assertHereCardGaps(row.tag)
    var button = named("hereContinue")
    var label = TestUtil.findFirst(button, function (n) { return n.text === "Continue in Claude Code" })
    verify(label.x >= 0 && label.x + label.width <= button.width && label.paintedWidth <= label.width + 0.5,
      row.tag + ": Continue label (x " + label.x + ", " + label.paintedWidth + " painted in " + label.width + ") outside its button (" + button.width + ")")
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
    var h = act.launchedCount(isHyprRun, 1, "focus window")
    act.drain()
    compare(agentRuns().length, 0)
    compare(hyprRuns().length, 1)
    compare(JSON.stringify(h[0].command), JSON.stringify(Launchers.focusWindowCommand("0x55aa")))
    compare(h[0].command[2], 'hl.dsp.focus({ window = "address:0x55aa" })')
  }

  function test_continue_without_window_launches_agent_in_cwd() {
    open(payload())
    clickText("Continue in Claude Code")
    var a = act.launchedCount(isTermRun, 1, "Continue launch")
    act.drain()
    compare(hyprRuns().length, 0)
    // foo's agents[0] is claude: the launch table, never omarchy agent prompt.
    compare(agentRuns().length, 0)
    compare(termRuns().length, 1)
    var prompt = a[0].command[a[0].command.length - 1]
    compare(JSON.stringify(a[0].command.slice(7, 10)), JSON.stringify(Launchers.LAUNCH_TABLE.claude.argv))
    verify(prompt.indexOf("handoff-0042") >= 0, prompt)
    verify(prompt.indexOf("Wire the bar") >= 0, prompt)
    compare(a[0].workingDirectory, "/home/brian/projects/foo")
  }

  function test_continue_with_hostile_window_launches_agent_never_hyprctl() {
    open(payload(function (d) { d.where_left_off[0].project.window = "0x1; rm" }))
    clickText("Continue in Claude Code")
    act.launchedCount(isTermRun, 1, "hostile-window Continue launch")
    act.drain()
    compare(hyprRuns().length, 0)
    compare(termRuns().length, 1)
  }

  function test_focus_falls_back_to_focuswindow_when_dispatch_fails() {
    open(payload(function (d) { d.where_left_off[0].project.window = "0x55aa" }))
    ProcessController.respond(Launchers.focusWindowCommand("0x55aa"), { stdout: "", stderr: "", exitCode: 1 })
    clickText("Continue in Claude Code")
    var h = act.launchedCount(isHyprRun, 2, "focus then focuswindow fallback")
    compare(h.length, 2)
    compare(JSON.stringify(h[1].command), JSON.stringify(["hyprctl", "dispatch", "focuswindow", "address:0x55aa"]))
  }

  // ---- (3) the old agent notice is gone; the unset default offers the picker ----
  function test_no_continue_opens_notice_when_last_agent_differs_from_default() {
    open(payload(function (d) { d.where_left_off[0].project.last_agent = "codex" }))
    compare(textContaining("Continue opens"), "")
  }

  function test_unset_default_offers_picker() {
    open(payload(function (d) {
      d.where_left_off[0].project.last_agent = ""
      d.where_left_off[0].project.agents = []
    }), "")
    verify(hasText("Choose default agent"))
    compare(textContaining("Continue opens"), "")
    clickText("Choose default agent")
    act.launchedCount(isAgentRun, 1, "agent picker")
    compare(agentRuns().length, 1)
    compare(JSON.stringify(agentRuns()[0].command), JSON.stringify(["omarchy", "agent", "--pick"]))
  }

  // ---- (4) Recent rows ----
  function strips(rowName) {
    var row = null
    act.settled(function () {
      row = TestUtil.findFirst(panel.testContentItem, function (n) { return n.summary && n.stripCounts !== undefined && n.summary.display_name === rowName })
      return row !== null && TestUtil.findAll(row, function (n) { return n.objectName === "stripCell" }).length > 0
    }, "no row " + rowName)
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

  // Recent rows: a row active within RECENT_ACTIVE_MINUTES shows a labelled
  // button ("Switch" with a window, else "Continue"); older rows only the glyph.
  function minutesAgo(m) { return new Date(Date.now() - m * 60000).toISOString() }

  function recentButtons() {
    return TestUtil.findAll(panel.testContentItem, function (n) { return n.objectName === "recentActionButton" && n.visible })
  }

  function test_recent_row_with_window_shows_switch() {
    open(payload(function (d) {
      d.where_left_off[0].project.last_activity = minutesAgo(5)
      d.where_left_off[0].project.window = "0x55aa"
      d.where_left_off[1].project.last_activity = minutesAgo(300)
      d.where_left_off[2].project.last_activity = minutesAgo(300)
    }))
    var b = recentButtons()
    compare(b.length, 1)
    compare(b[0].label, "Switch")
    verify(hasText("Switch"))
  }

  function test_recent_row_without_window_shows_continue() {
    open(payload(function (d) {
      d.where_left_off[0].project.last_activity = minutesAgo(5)
      d.where_left_off[1].project.last_activity = minutesAgo(300)
      d.where_left_off[2].project.last_activity = minutesAgo(300)
    }))
    var b = recentButtons()
    compare(b.length, 1)
    compare(b[0].label, "Continue")
  }

  function test_old_row_shows_only_the_glyph_button() {
    open(payload(function (d) {
      d.where_left_off.forEach(function (r) { r.project.last_activity = minutesAgo(30) })
      d.where_left_off[0].project.window = "0x55aa"
    }))
    compare(recentButtons().length, 0)
    verify(!hasText("Switch"))
    verify(!hasText("Continue"), "labelled Continue shown for a 30m-old row")
  }

  function test_threshold_is_a_named_constant() {
    compare(Format.RECENT_ACTIVE_MINUTES, 15)
    compare(Format.recentActionLabel(minutesAgo(14), Date.now(), false), "Continue")
    compare(Format.recentActionLabel(minutesAgo(16), Date.now(), false), "")
  }

  function test_switch_button_continues_with_window_and_tmux_target() {
    open(payload(function (d) {
      d.where_left_off[0].project.last_activity = minutesAgo(5)
      d.where_left_off[0].project.window = "0x55aa"
      d.where_left_off[0].project.tmux = "Work:1.2"
    }))
    clickText("Switch")
    act.launchedCount(isHyprRun, 1, "Switch focus")
    act.launchedCount(isRun(["tmux", "select-window"]), 1, "tmux select-window")
    act.launchedCount(isRun(["tmux", "select-pane"]), 1, "tmux select-pane")
    act.drain()
    compare(hyprRuns().length, 1)
    compare(runs(["tmux", "select-window"]).length, 1)
    compare(runs(["tmux", "select-pane"]).length, 1)
    compare(agentRuns().length, 0)
    compare(runs(["xdg-terminal-exec", "--"]).length, 0)
  }

  // ---- (5) keyboard ----
  function press(key) {
    panel.testKeyItem.forceActiveFocus()
    act.pressKey(key)
  }

  // The fixture plus 25 extra Recent rows, so the card overflows; the last
  // row has six agents, so its expanded sub-rows run past the bottom edge.
  function longRecentPayload() {
    return payload(function (d) {
      for (var i = 0; i < 25; i++) {
        var row = JSON.parse(JSON.stringify(d.where_left_off[0]))
        row.project.project_key = "extra-" + i
        row.project.display_name = "extra-" + i
        row.project.window = ""
        if (i === 24) row.project.agents = ["claude", "codex", "copilot", "hermes", "opencode", "pi"].map(function (a) {
          return { agent: a, last_activity: row.project.agents[0].last_activity, session_count: 1 }
        })
        d.where_left_off.push(row)
      }
    })
  }

  // j/k (and the arrows) move the highlight through Recent; the highlighted
  // row must stay in view, scrolling the card as it goes past either edge.
  // ---- Snapshot and restore (the bar popup's pop-out into the window) ----
  function reopen(data) {
    panel.close()
    ProcessController.respond(Launchers.thisWeekCommand(), { stdout: JSON.stringify(data), stderr: "", exitCode: 0 })
    panel.open("{}")
  }

  function test_snapshot_names_the_view_selection_and_scroll() {
    open(longRecentPayload())
    press(Qt.Key_Down)
    press(Qt.Key_Down)
    var snap = panel.view.snapshot()
    compare(snap.view, "main")
    compare(snap.selectedId, panel.view.selectedId)
    compare(snap.scrollY, panel.testScroller.contentY)
    panel.view.helpOpen = true
    compare(panel.view.snapshot().view, "help")
  }

  function test_restore_keeps_the_selection_through_the_data_load() {
    var data = longRecentPayload()
    open(data)
    var start = panel.view.selectedIndex
    var want = start === 3 ? 2 : 3
    panel.view.selectedIndex = want
    var snap = JSON.parse(JSON.stringify(panel.view.snapshot()))
    // The test harness answers a run at once, so mark a load in flight by
    // hand: restore() must wait for it rather than select on stale rows.
    panel.view.selectedIndex = start
    panel.view.loading = true
    panel.view.restore(snap)
    compare(panel.view.selectedIndex, start, "restore selected before the data arrived")
    panel.view.refresh()
    act.settledEq(panel.view, "selectedIndex", want)
  }

  function test_restore_scrolls_back() {
    var data = longRecentPayload()
    open(data)
    var scroller = panel.testScroller
    scroller.contentY = Math.min(200, scroller.contentHeight - scroller.height)
    var snap = JSON.parse(JSON.stringify(panel.view.snapshot()))
    verify(snap.scrollY > 0, "fixture does not scroll")
    reopen(data)
    panel.testScroller.contentY = 0
    panel.view.restore(snap)
    act.settled(function () { return Math.abs(panel.testScroller.contentY - snap.scrollY) < 1 },
      "never scrolled back to " + snap.scrollY)
  }

  function test_restore_opens_memory_for_that_project() {
    open(payload())
    panel.view.restore({ view: "memory", memory: { projectKey: "k-1", displayName: "kay", location: "/x/k", state: { history: false, expanded: [] } } })
    compare(panel.view.memoryOpen, true)
    compare(panel.view.memoryProjectKey, "k-1")
    compare(panel.view.memoryDisplayName, "kay")
    compare(panel.view.memoryLocation, "/x/k")
  }

  function test_restore_opens_groups() {
    open(payload())
    panel.view.restore({ view: "groups" })
    compare(panel.view.groupEditorOpen, true)
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "groupsPage") }, "Groups never showed")
  }

  function test_restore_ignores_bad_values() {
    open(payload())
    panel.view.restore(null)
    panel.view.restore([1, 2])
    panel.view.restore({ view: "memory", memory: { projectKey: 7 }, selectedId: {}, expandedRows: "x", expandedGroups: [1, null], scrollY: "far" })
    compare(panel.view.memoryOpen, false)
    compare(panel.view.groupEditorOpen, false)
    compare(panel.view.helpOpen, false)
    compare(JSON.stringify(panel.view.expandedRows), "{}")
    compare(JSON.stringify(panel.view.expandedGroups), "{}")
  }

  function test_keyboard_selection_stays_in_view() {
    open(longRecentPayload())
    var scroller = panel.testScroller
    verify(scroller.contentHeight > scroller.height + 200, "fixture does not overflow the card: content " + scroller.contentHeight + ", view " + scroller.height)
    function checkInView(step) {
      var row = TestUtil.findFirst(panel.testContentItem, function (n) { return n.selected === true })
      verify(row !== null, step + ": no selected row found")
      var top = row.mapToItem(scroller, 0, 0).y
      verify(top >= -1 && top + row.height <= scroller.height + 1,
             step + ": selected row spans " + top + ".." + (top + row.height) + ", view is 0.." + scroller.height)
    }
    var last = panel.view.navRows.length - 1
    for (var i = 1; i <= last; i++) {
      press(Qt.Key_J)
      act.settledEq(panel.view, "selectedIndex", i)
      checkInView("j to row " + i)
    }
    // Into the last row's agent sub-rows: expand it, then j through them.
    press(Qt.Key_Right)
    tryVerify(function () { return panel.view.navRows.length > last + 1 }, 2000, "expanding the last row added no agent sub-rows")
    for (var a = last + 1; a < panel.view.navRows.length; a++) {
      press(Qt.Key_J)
      act.settledEq(panel.view, "selectedIndex", a)
      checkInView("j to agent sub-row " + a)
    }
    last = panel.view.navRows.length - 1
    for (var k = last - 1; k >= 0; k--) {
      press(Qt.Key_K)
      act.settledEq(panel.view, "selectedIndex", k)
      checkInView("k to row " + k)
    }
  }

  // The header stays put; HERE, Needs you and Recent scroll under it.
  function test_header_stays_put_while_here_and_the_lists_scroll() {
    open(longRecentPayload())
    function y(item) { return item.mapToItem(panel.testContentItem, 0, 0).y }
    var title = TestUtil.findFirst(panel.testContentItem, function (n) { return n.text === "Backstory" })
    var here = TestUtil.findFirst(panel.testContentItem, function (n) { return n.objectName === "hereCard" })
    verify(title !== null && here !== null, "no header title or HERE card")
    var titleY = y(title), hereY = y(here)
    var scroller = panel.testScroller
    scroller.contentY = Math.min(200, scroller.contentHeight - scroller.height)
    verify(scroller.contentY > 100, "fixture too short to scroll")
    act.quiescent(function () { return [scroller.contentY, y(title), y(here)] }, "never stopped scrolling")
    verify(Math.abs(y(title) - titleY) < 0.5, "the header scrolled: " + titleY + " -> " + y(title))
    verify(y(here) < hereY - 100, "the HERE card did not scroll")
  }

  // Back on the first Recent row, up (k or the arrow) scrolls the whole card
  // to the top, so the HERE card and Needs you are in view again.
  function test_up_on_first_row_scrolls_to_top() {
    open(longRecentPayload())
    var scroller = panel.testScroller
    var last = panel.view.navRows.length - 1
    for (var i = 1; i <= last; i++) press(Qt.Key_J)
    act.settledEq(panel.view, "selectedIndex", last)
    for (var k = last - 1; k >= 0; k--) press(Qt.Key_K)
    act.settledEq(panel.view, "selectedIndex", 0)
    verify(scroller.contentY > 0, "first row already shows the top of the card (contentY " + scroller.contentY + "): fixture too short")
    press(Qt.Key_K)
    act.settledEq(scroller, "contentY", 0)
    compare(panel.view.selectedIndex, 0)
    press(Qt.Key_Up)
    act.settledEq(scroller, "contentY", 0)
    compare(panel.view.selectedIndex, 0)
  }

  // ---- The selection is a row, not a place in the list ----
  function selectedName() { return panel.view.selectedSummary ? panel.view.selectedSummary.display_name : null }

  function reloadWith(data) {
    ProcessController.respond(Launchers.thisWeekCommand(), { stdout: JSON.stringify(data), stderr: "", exitCode: 0 })
    press(Qt.Key_R)
    tryCompare(panel.view, "loading", false, 2000)
  }

  function test_expanding_a_row_above_keeps_the_selection_on_its_row() {
    open(payload())
    press(Qt.Key_J)
    press(Qt.Key_J)
    compare(selectedName(), "projects/notes")
    act.clickWhenReady(function () { return named("hereAgentChips") },
      { desc: "the HERE card's agents", until: function () { return panel.view.navRows.length === 4 } })
    compare(selectedName(), "projects/notes", "expanding foo's agents moved the selection")
    compare(panel.view.selectedAgent, "")
    act.moveAway(panel.testContentItem)
  }

  function test_a_reload_keeps_the_selection_on_its_row() {
    open(payload())
    press(Qt.Key_J)
    compare(selectedName(), "bar")
    reloadWith(payload(function (d) { d.where_left_off.push(d.where_left_off.splice(1, 1)[0]) }))
    compare(panel.view.navRows[2].summary.display_name, "bar", "the reload did not move bar")
    compare(selectedName(), "bar", "the reload moved the selection off its row")
  }

  function test_a_reload_without_the_selected_row_selects_the_next_one() {
    open(payload())
    press(Qt.Key_J)
    compare(selectedName(), "bar")
    reloadWith(payload(function (d) { d.where_left_off.splice(1, 1) }))
    compare(selectedName(), "projects/notes")
  }

  function test_opening_the_panel_again_selects_here() {
    open(payload())
    press(Qt.Key_J)
    compare(selectedName(), "bar")
    panel.close()
    open(payload())
    compare(selectedName(), "projects/foo")
  }

  function test_keys_select_continue_terminal_memory_and_close() {
    open(payload())
    act.settledEq(panel.view, "selectedIndex", 0)
    press(Qt.Key_J)
    act.settledEq(panel.view, "selectedIndex", 1)
    // row 2 is bar (cwd /home/brian/bar), codex, no handoff
    press(Qt.Key_Return)
    var a = act.launchedCount(isTermRun, 1, "Enter on bar")
    compare(a[0].workingDirectory, "/home/brian/bar")
    compare(JSON.stringify(a[0].command.slice(7, 9)), JSON.stringify(Launchers.LAUNCH_TABLE.codex.argv))
    verify(a[0].command[a[0].command.length - 1].indexOf("bar") >= 0)

    press(Qt.Key_T)
    var last = act.detachedNext(function (c) { return JSON.stringify(c.command) === JSON.stringify(Launchers.terminalLaunchCommand("/home/brian/bar")) }, "T opens terminal")
    compare(JSON.stringify(last.command), JSON.stringify(Launchers.terminalLaunchCommand("/home/brian/bar")))
    compare(last.workingDirectory, "/home/brian/bar")

    press(Qt.Key_M)
    act.settledEq(panel.view, "memoryOpen", true)
    act.settledEq(panel.view, "memoryProjectKey", "bar-key")
    act.settledEq(panel.view, "memoryLocation", "/home/brian/bar")

    press(Qt.Key_Escape)
    act.settledEq(panel, "opened", false)
    act.settledEq(panel.view, "memoryOpen", false)
  }

  function recentRows() {
    return TestUtil.findAll(panel.testContentItem, function (n) { return n.visible && n.summary && n.stripCounts !== undefined })
  }

  function test_hovering_a_row_moves_the_one_selection() {
    open(payload())
    var rows = recentRows()
    verify(rows.length >= 3, "expected at least 3 recent rows, got " + rows.length)
    act.hover(rows[2], { until: function () { return panel.view.selectedIndex === 2 }, desc: "hovering row 2" })
    var lit = recentRows().filter(function (r) { return r.selected === true })
    compare(lit.length, 1)
    compare(lit[0], rows[2])
    // A cursor left over a row would select it in the next test's panel.
    act.moveAway(panel.testContentItem)
  }

  // The pointer rests over row 1 while the keys move on, open Memory and
  // come back: the row sliding back under the still pointer isn't a hover.
  function test_rows_sliding_under_a_still_pointer_keep_the_keys_selection() {
    open(payload())
    var rows = recentRows()
    act.hover(rows[1], { until: function () { return panel.view.selectedIndex === 1 }, desc: "hovering row 1" })
    press(Qt.Key_J)
    act.settledEq(panel.view, "selectedIndex", 2)
    press(Qt.Key_M)
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "memoryPage") }, "Memory never slid in")
    press(Qt.Key_U)
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "mainPage") }, "main never slid back")
    wait(200)
    compare(panel.view.selectedIndex, 2)
    act.moveAway(panel.testContentItem)
  }

  // j scrolls the list under the still pointer; the rows passing under it
  // aren't hovers.
  function test_rows_scrolling_under_a_still_pointer_keep_the_keys_selection() {
    open(longRecentPayload())
    var rows = recentRows()
    act.hover(rows[1], { until: function () { return panel.view.selectedIndex === 1 }, desc: "hovering row 1" })
    var top = panel.testScroller.contentY
    var n = 1
    while (panel.testScroller.contentY === top && n < panel.view.navRows.length - 1) {
      press(Qt.Key_J)
      n++
      act.settledEq(panel.view, "selectedIndex", n)
    }
    verify(panel.testScroller.contentY > top, "j never scrolled the list")
    wait(200)
    compare(panel.view.selectedIndex, n)
    act.moveAway(panel.testContentItem)
  }

  // The pointer moved off the rows (onto the top of the card) and rests
  // there; j scrolls rows up under it. The gate last saw it on row 1, so
  // without a reset on each key the row arriving would count as a move.
  function test_rows_scrolling_under_a_pointer_resting_off_the_rows_keep_the_keys_selection() {
    open(longRecentPayload())
    var rows = recentRows()
    act.hover(rows[1], { until: function () { return panel.view.selectedIndex === 1 }, desc: "hovering row 1" })
    act.hover(panel.testScroller, { x: 40, y: 4 })
    var n = 1
    function rowUnderPointer() {
      return recentRows().some(function (r) { var p = r.mapFromItem(panel.testScroller, 40, 4); return p.y >= 0 && p.y < r.height })
    }
    while (!rowUnderPointer() && n < panel.view.navRows.length - 1) {
      press(Qt.Key_J)
      n++
      act.settledEq(panel.view, "selectedIndex", n)
    }
    verify(rowUnderPointer(), "j never scrolled a row under the pointer")
    wait(200)
    compare(panel.view.selectedIndex, n)
    act.moveAway(panel.testContentItem)
  }

  // The pointer moves off the rows and rests below them; a reload brings
  // more rows under it. Rows arriving under a still pointer aren't a hover:
  // the selection stays where the load put it.
  function test_rows_reloading_under_a_pointer_resting_below_them_keep_the_loads_selection() {
    open(payload())
    var rows = recentRows()
    act.hover(rows[1], { until: function () { return panel.view.selectedIndex === 1 }, desc: "hovering row 1" })
    var last = rows[rows.length - 1]
    var restY = Math.min(last.mapToItem(panel.testScroller, 0, last.height).y + 20, panel.testScroller.height - 5)
    function rowUnderPointer() {
      return recentRows().some(function (r) { var p = r.mapFromItem(panel.testScroller, 40, restY); return p.y >= 0 && p.y < r.height })
    }
    act.hover(panel.testScroller, { x: 40, y: restY })
    verify(!rowUnderPointer(), "the pointer rests on a row")
    ProcessController.respond(Launchers.thisWeekCommand(), { stdout: JSON.stringify(longRecentPayload()), stderr: "", exitCode: 0 })
    panel.view.refresh()
    tryCompare(panel.view, "loading", false, 2000)
    var loaded = panel.view.selectedIndex
    act.settled(rowUnderPointer, "the reload never put a row under the pointer")
    wait(200)
    compare(panel.view.selectedIndex, loaded)
    act.moveAway(panel.testContentItem)
  }

  // After a key, the first real move onto another row counts at once.
  function test_a_real_move_after_a_key_moves_the_selection() {
    open(payload())
    var rows = recentRows()
    act.hover(rows[1], { until: function () { return panel.view.selectedIndex === 1 }, desc: "hovering row 1" })
    press(Qt.Key_J)
    act.settledEq(panel.view, "selectedIndex", 2)
    act.hover(rows[0], { until: function () { return panel.view.selectedIndex === 0 }, desc: "moving onto row 0" })
    act.moveAway(panel.testContentItem)
  }

  function test_needs_you_shows_the_thing_then_a_label_and_details() {
    open(payload(function (d) {
      d.attention.push({ kind: "possibly-stale-handoff", project_key: "foo", reason: "foo: handoff possibly stale — cargo test failed (exit 101) after it", handoff_id: "h-foo", evidence_ids: ["7"] })
    }))
    verify(hasText("foo: handoff possibly stale"), "attention line missing: " + JSON.stringify(texts()))
    verify(hasText("Command failed."), "detail label missing")
    verify(hasText("Details"), "Details link missing")
    compare(textContaining("cargo test failed"), "", "the raw detail belongs in the Details tooltip, not the panel")
  }

  function staleHandoff(key) {
    return { kind: "possibly-stale-handoff", project_key: key, reason: key + ": handoff possibly stale", handoff_id: "h-" + key, evidence_ids: ["7"] }
  }

  function heroStatusText() {
    var n = TestUtil.findFirst(panel.testContentItem, function (n) { return n.objectName === "heroStatus" })
    return n ? n.text : null
  }

  function test_hero_status_counts_what_needs_you_in_number() {
    open(payload(function (d) { d.attention = [staleHandoff("foo")] }))
    compare(heroStatusText(), "1 needs you")
  }

  function test_hero_status_plural_for_more_than_one() {
    open(payload(function (d) { d.attention = [staleHandoff("foo"), staleHandoff("bar")] }))
    compare(heroStatusText(), "2 need you")
  }

  function test_k_and_arrows_move_and_clamp() {
    open(payload())
    press(Qt.Key_Down)
    press(Qt.Key_Down)
    press(Qt.Key_Down)
    act.settledEq(panel.view, "selectedIndex", 2)
    press(Qt.Key_K)
    act.settledEq(panel.view, "selectedIndex", 1)
    press(Qt.Key_Up)
    press(Qt.Key_Up)
    act.settledEq(panel.view, "selectedIndex", 0)
  }

  function test_r_refreshes() {
    open(payload())
    var before = runs(["backstory", "this-week"]).length
    press(Qt.Key_R)
    act.launchedCount(isRun(["backstory", "this-week"]), before + 1, "R refresh")
    act.drain()
    compare(runs(["backstory", "this-week"]).length, before + 1)
  }

  function test_row_keys_inert_while_memory_view_open() {
    open(payload())
    panel.view.openMemory("foo-key", "projects/foo", "/home/brian/projects/foo")
    act.settledEq(panel.view, "memoryOpen", true)
    var before = Quickshell.execDetachedCalls.length
    press(Qt.Key_T)
    act.drain()
    compare(Quickshell.execDetachedCalls.length, before)
    press(Qt.Key_Escape)
    act.settledEq(panel, "opened", false)
  }

  function test_footer_lists_the_keys() {
    open(payload())
    var f = textContaining("Esc")
    verify(f.indexOf("continue") >= 0 && f.indexOf("terminal") >= 0 && f.indexOf("memory") >= 0, f)
  }

  // ---- (7) layout gates ----
  function assertWithinBounds(label) {
    var bad = []
    act.settled(function () {
      bad = TestUtil.collectOutOfBounds(panel.testContentItem, panel.testContentItem, panel.testImplicitWidth, panel.testImplicitHeight)
      return bad.length === 0
    }, label + ": " + JSON.stringify(bad).slice(0, 1500))
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
    panel.view.openMemory("foo-key", "projects/foo", "/home/brian/projects/foo")
    act.settledEq(panel.view, "memoryOpen", true)
    assertWithinBounds("memory open")
  }

  function test_layout_group_editor_open() {
    open(payload())
    ProcessController.respond(Launchers.groupListCommand(), { stdout: JSON.stringify({ groups: [], ungrouped: [] }), stderr: "", exitCode: 0 })
    panel.view.groupEditorOpen = true
    act.settledEq(panel.view, "groupEditorOpen", true)
    assertWithinBounds("group editor open")
  }
}
