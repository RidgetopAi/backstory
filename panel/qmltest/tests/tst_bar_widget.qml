import QtQuick
import QtTest
import Quickshell
import Quickshell.Io
import Quickshell.Hyprland
import "testutil.js" as TestUtil
import "../../js/launchers.js" as Launchers

// BarWidget.qml: shows the `here` project's short name (glyph only when
// there is none) and an attention dot iff Attention is non-empty. Left
// click toggles its popup (a KeyboardPanel holding ThisWeekView), right
// click is Continue on `here`, middle click a refresh (task fc1f340d).
TestCase {
  id: testCase
  name: "BarWidget"
  when: windowShown
  visible: true
  width: 300
  height: 100

  readonly property string fixtureUrl: Qt.resolvedUrl("../../testdata/this-week-here.json")

  Component {
    id: barWidgetComponent
    Loader { source: Qt.resolvedUrl("../../BarWidget.qml") }
  }

  property var loader: null

  TestActions { id: act; testCase: testCase }

  function fixture(mutate) {
    var xhr = new XMLHttpRequest()
    xhr.open("GET", fixtureUrl, false)
    xhr.send()
    var data = JSON.parse(xhr.responseText.replace(/@NOW/g, new Date().toISOString()).replace(/@D\d/g, "2026-01-01"))
    if (mutate) mutate(data)
    return data
  }

  // The widget fetches on creation, so the canned responses go in first.
  function start(data, defaultAgent) {
    ProcessController.reset()
    Quickshell.reset()
    Hyprland.reset()
    ProcessController.respond(Launchers.thisWeekCommand(), { stdout: JSON.stringify(data), stderr: "", exitCode: 0 })
    ProcessController.respond(Launchers.defaultAgentCommand(), { stdout: defaultAgent === undefined ? "claude\n" : defaultAgent, stderr: "", exitCode: 0 })
    loader = barWidgetComponent.createObject(testCase)
    tryCompare(loader, "status", Loader.Ready, 2000)
    verify(loader.item !== null, "BarWidget.qml failed to load: status=" + loader.status)
    act.launchedNext(function (r) { return ProcessController.keyFor(r.command) === ProcessController.keyFor(Launchers.thisWeekCommand()) }, "initial this-week fetch")
    act.drain()
    return loader.item
  }

  function cleanup() {
    if (loader) loader.destroy()
    loader = null
  }

  function weekRuns() {
    return ProcessController.runs.filter(function (r) { return r.command[0] === "backstory" })
  }

  function barLabel(widget) {
    return TestUtil.findFirst(widget, function (n) { return n.objectName === "barLabel" })
  }

  function labelShows(widget, text) {
    act.settled(function () { var l = barLabel(widget); return l !== null && l.text === text }, "barLabel never showed " + JSON.stringify(text))
  }

  function dot(widget) {
    return TestUtil.findFirst(widget, function (n) { return n.objectName === "attentionDot" })
  }

  function test_shows_here_project_name() {
    var widget = start(fixture())
    labelShows(widget, "foo")
  }

  // With BACKSTORY_PROJECT_ROOTS set, `backstory this-week --json --here auto`
  // answers `recent` with the most recent IN-SCOPE project (cmd/backstory's
  // TestHereNamesMostRecentInScopeProject) even when a newer out-of-scope
  // session exists; the bar widget shows that name.
  function test_shows_most_recent_in_scope_project_name() {
    var widget = start(fixture(function (d) {
      d.here = { project_key: "scoped-key", display_name: "projects/scoped", cwd: "/home/brian/projects/scoped", source: "recent" }
    }))
    labelShows(widget, "scoped")
  }

  function test_attention_dot_iff_attention_non_empty() {
    var widget = start(fixture())
    act.settled(function () { return dot(widget) !== null }, "dot should be visible with attention")
    cleanup()
    widget = start(fixture(function (d) { d.attention = [] }))
    // Negative: the dot must stay absent once the data (the label) has rendered.
    act.settled(function () { return barLabel(widget) !== null && barLabel(widget).text !== "" }, "label never rendered")
    compare(dot(widget), null)
  }

  // The bar lines its slots up at their top, so a widget shorter than the
  // bar sits above the icons beside it: it takes the bar's own thickness.
  function test_fills_the_bar_thickness() {
    var widget = start(fixture())
    widget.bar = fakeBar(31)
    compare(widget.implicitHeight, 31)
  }

  function test_glyph_only_when_no_here() {
    var widget = start(fixture(function (d) { delete d.here }))
    var glyph = TestUtil.findFirst(widget, function (n) { return n.objectName === "barGlyph" })
    verify(glyph !== null && glyph.text !== "", "glyph never rendered")
    verify(barLabel(widget) === null, "label shown with no here project")
  }

  // The bar a Panel root is given: barSize, the label font, and the
  // panel-switching hook Tab calls (it records each direction).
  function fakeBar(size) {
    return Qt.createQmlObject('import QtQuick; QtObject { property int barSize: ' + size
      + '; property string fontFamily: "sans-serif"; property string position: "top"; property var switched: []; '
      + 'function switchPanelFrom(panel, direction) { switched = switched.concat([direction]); return true } }', testCase)
  }

  function openPopup(widget) {
    act.clickWhenReady(widget, { button: Qt.LeftButton, desc: "bar widget", until: function () { return widget.opened } })
    act.settled(function () { return widget.testPopup.loadedContent !== null }, "popup content never built")
    return widget.testPopup.loadedContent
  }

  function test_left_click_opens_the_popup_with_this_week() {
    var widget = start(fixture())
    var content = openPopup(widget)
    verify(typeof content.handleKey === "function", "popup content has no key handler")
    compare(Quickshell.execDetachedCalls.length, 0, "a left click ran a command: " + JSON.stringify(Quickshell.execDetachedCalls))
    act.clickWhenReady(widget, { button: Qt.LeftButton, desc: "bar widget again", until: function () { return !widget.opened } })
  }

  // With the window open, the icon focuses it instead of opening the popup,
  // so the two never show together; a second click focuses it again.
  function test_left_click_focuses_the_open_window_instead_of_the_popup() {
    var widget = start(fixture())
    Hyprland.toplevels = { values: [{ address: "55aa", title: "backstory", activated: false, workspace: { id: 2 }, wayland: { appId: "org.quickshell" } }] }
    for (var i = 0; i < 2; i++) {
      act.clickWhenReady(widget, { button: Qt.LeftButton, desc: "bar widget", until: function () { return Quickshell.execDetachedCalls.length === i + 1 } })
      compare(JSON.stringify(Quickshell.execDetachedCalls[i].command), JSON.stringify(Launchers.windowFocusCommand()))
    }
    wait(50)
    compare(widget.opened, false, "the popup opened beside the window")
  }

  // Another app's window titled "backstory" is not ours.
  function test_left_click_opens_the_popup_beside_another_apps_backstory_window() {
    var widget = start(fixture())
    Hyprland.toplevels = { values: [{ address: "55aa", title: "backstory", activated: false, workspace: { id: 2 }, wayland: { appId: "org.other" } }] }
    openPopup(widget)
    compare(Quickshell.execDetachedCalls.length, 0)
  }

  function test_esc_in_the_popup_closes_it() {
    var widget = start(fixture())
    var content = openPopup(widget)
    verify(content.handleKey(Qt.Key_Escape, Qt.NoModifier))
    compare(widget.opened, false)
  }

  // Real keys: the popup's content takes focus as the popup opens, and its
  // own key handler sends them to the view (Esc closes).
  function test_keys_typed_in_the_popup_reach_its_view() {
    var widget = start(fixture())
    widget.testPopup.testScene = testCase
    var content = openPopup(widget)
    act.settled(function () { return content.activeFocus }, "the popup's content never took focus")
    act.pressKey(Qt.Key_Escape)
    compare(widget.opened, false, "Esc typed in the popup did not close it")
  }

  // One bar per monitor, each with the popup's IPC target: only the one on
  // the focused monitor answers the shortcut.
  function test_only_the_bar_on_the_focused_monitor_answers_the_shortcut() {
    var widget = start(fixture())
    var ipc = null
    for (var i = 0; i < widget.resources.length; i++)
      if (widget.resources[i].target === "backstory.this-week.popup") ipc = widget.resources[i]
    verify(ipc !== null, "no popup IPC target")
    widget.testPopup.screen = { name: "DP-1", x: 0, y: 0, width: 2560, height: 1440 }
    Hyprland.focusedMonitor = { name: "DP-1" }
    compare(ipc.enabled, true, "the bar on the focused monitor does not answer")
    Hyprland.focusedMonitor = { name: "HDMI-A-1" }
    compare(ipc.enabled, false, "a bar on another monitor answers")
  }

  function test_o_pops_out_from_a_sub_view() {
    var widget = start(fixture())
    var content = openPopup(widget)
    var view = TestUtil.findFirst(content, function (n) { return n.canPopOut !== undefined })
    view.memoryOpen = true
    verify(view.footerHint.indexOf("o pop out") >= 0, "footer never listed o: " + view.footerHint)
    verify(content.handleKey(Qt.Key_O, Qt.NoModifier))
    var run = act.detachedNext(function (c) { return c.command[2] === "summon" }, "window summon")
    compare(JSON.parse(run.command[4]).view, "memory")
  }

  function test_pop_out_summons_the_window_where_the_popup_is() {
    var widget = start(fixture())
    var content = openPopup(widget)
    // The popup's content is outside the test scene, so the button can't be
    // clicked here; tst_panel's test_pop_out_button_asks_the_host clicks it.
    var view = TestUtil.findFirst(content, function (n) { return n.canPopOut !== undefined })
    verify(view !== null && view.canPopOut === true, "the popup's view offers no pop-out")
    verify(TestUtil.findFirst(content, function (n) { return n.tooltipText === "Pop out" }) !== null, "no pop-out button in the popup")
    compare(TestUtil.findFirst(content, function (n) { return n.objectName === "pinButton" }), null, "the popup offers a pin")
    widget.bar = fakeBar(26)
    widget.bar.position = "left"
    widget.testPopup.screen = { x: 0, y: 0, width: 4096, height: 2160 }
    widget.testPopup.cardOrigin = Qt.point(3700, 34)
    widget.testPopup.barThickness = 26
    widget.testPopup.gap = 8
    view.helpOpen = true
    view.popOutRequested()
    var run = act.detachedNext(function (c) { return c.command[2] === "summon" }, "window summon")
    compare(JSON.stringify(run.command.slice(0, 4)), JSON.stringify(["omarchy-shell", "shell", "summon", "backstory.this-week"]))
    // The window opens where you were in the popup, and where its card is.
    var payload = JSON.parse(run.command[4])
    compare(payload.view, "help")
    compare(JSON.stringify(payload.from), JSON.stringify({ x: 3700, y: 34, w: widget.testPopup.contentWidth, h: widget.testPopup.drawnHeight, bar: 26, gap: 8, monX: 0, monY: 0, monW: 4096, monH: 2160, edge: "left" }))
  }

  // On a monitor right of and below another, the card's place is in screen
  // coordinates: the monitor's origin plus where the card sits on it.
  function test_pop_out_from_a_monitor_off_the_origin_sends_screen_coordinates() {
    var widget = start(fixture())
    var content = openPopup(widget)
    widget.testPopup.screen = { name: "DP-2", x: 2560, y: 360, width: 1920, height: 1080 }
    widget.testPopup.cardOrigin = Qt.point(1500, 34)
    verify(content.handleKey(Qt.Key_O, Qt.NoModifier))
    var run = act.detachedNext(function (c) { return c.command[2] === "summon" }, "window summon")
    var from = JSON.parse(run.command[4]).from
    compare([from.x, from.y, from.monX, from.monY, from.monW, from.monH], [4060, 394, 2560, 360, 1920, 1080])
  }

  // The popup stays up until the window, in its place, closes it, so
  // nothing blinks between them.
  function test_popup_stays_up_until_the_window_closes_it() {
    var widget = start(fixture())
    var content = openPopup(widget)
    verify(content.handleKey(Qt.Key_O, Qt.NoModifier))
    act.detachedNext(function (c) { return c.command[2] === "summon" }, "window summon")
    compare(widget.opened, true, "the popup closed before the window took its place")
    widget.closeAfterPopOut()
    compare(widget.opened, false)
  }

  // If the window never takes its place, the popup closes anyway.
  function test_popup_closes_when_the_window_never_comes() {
    var widget = start(fixture())
    widget.popOutGiveUpMs = 100
    var content = openPopup(widget)
    verify(content.handleKey(Qt.Key_O, Qt.NoModifier))
    compare(widget.opened, true)
    act.settledEq(widget, "opened", false)
  }

  function summons() {
    return Quickshell.execDetachedCalls.filter(function (c) { return c.command[2] === "summon" }).length
  }

  // p twice (or p then the button) sends one summon: a second would find
  // the window open but still hidden, and close the popup at once.
  function test_a_second_pop_out_while_one_is_pending_sends_nothing() {
    var widget = start(fixture())
    var content = openPopup(widget)
    verify(content.handleKey(Qt.Key_O, Qt.NoModifier))
    act.detachedNext(function (c) { return c.command[2] === "summon" }, "window summon")
    verify(content.handleKey(Qt.Key_O, Qt.NoModifier))
    wait(50)
    compare(summons(), 1, "a second pop-out was sent while the first was pending")
  }

  // Closed by hand while a pop-out is pending, the popup is done with it: a
  // pop-out from the popup opened again goes out, and the first one's
  // give-up does not close the new popup.
  function test_closing_the_popup_by_hand_ends_its_pending_pop_out() {
    var widget = start(fixture())
    widget.popOutGiveUpMs = 300
    var content = openPopup(widget)
    verify(content.handleKey(Qt.Key_O, Qt.NoModifier))
    act.detachedNext(function (c) { return c.command[2] === "summon" }, "window summon")
    verify(content.handleKey(Qt.Key_Escape, Qt.NoModifier))
    compare(widget.opened, false)
    content = openPopup(widget)
    verify(content.handleKey(Qt.Key_O, Qt.NoModifier))
    compare(summons(), 2, "the reopened popup's pop-out was not sent")
  }

  // The other half: with no pop-out from the reopened popup, the first
  // one's give-up passes and the popup stays up.
  function test_a_popup_opened_again_outlives_the_closed_ones_give_up() {
    var widget = start(fixture())
    widget.popOutGiveUpMs = 300
    var content = openPopup(widget)
    verify(content.handleKey(Qt.Key_O, Qt.NoModifier))
    act.detachedNext(function (c) { return c.command[2] === "summon" }, "window summon")
    verify(content.handleKey(Qt.Key_Escape, Qt.NoModifier))
    compare(widget.opened, false)
    openPopup(widget)
    wait(widget.popOutGiveUpMs + 200)
    verify(widget.opened, "the first pop-out's give-up closed the reopened popup")
  }

  function test_tab_moves_to_the_next_bar_panel() {
    var widget = start(fixture())
    widget.bar = fakeBar(26)
    var content = openPopup(widget)
    verify(content.handleKey(Qt.Key_Tab, Qt.NoModifier))
    verify(content.handleKey(Qt.Key_Backtab, Qt.ShiftModifier))
    compare(widget.bar.switched, [1, -1])
  }

  function test_label_drops_projects_prefix() {
    var widget = start(fixture())
    labelShows(widget, "foo")
  }

  function test_right_click_is_continue_on_here() {
    var widget = start(fixture())
    act.clickWhenReady(widget, { button: Qt.RightButton, desc: "bar widget (right)" })
    var run = act.launchedNext(function (r) { return r.command[0] === "xdg-terminal-exec" }, "right click Continue launch")
    compare(Quickshell.execDetachedCalls.length, 0)
    // foo's agents[0] is claude: launched from the launch table, not omarchy.
    compare(JSON.stringify(run.command.slice(0, 3)), JSON.stringify(["xdg-terminal-exec", "--", "sh"]))
    compare(JSON.stringify(run.command.slice(7, 10)), JSON.stringify(Launchers.LAUNCH_TABLE.claude.argv))
    var prompt = run.command[run.command.length - 1]
    verify(prompt.indexOf("handoff-0042") >= 0 && prompt.indexOf("Wire the bar") >= 0)
    compare(run.workingDirectory, "/home/brian/projects/foo")
  }

  function test_right_click_focuses_the_open_window() {
    var widget = start(fixture(function (d) { d.where_left_off[0].project.window = "0x55aa" }))
    act.clickWhenReady(widget, { button: Qt.RightButton, desc: "bar widget (right)" })
    var run = act.launchedNext(function (r) { return JSON.stringify(r.command) === JSON.stringify(Launchers.focusWindowCommand("0x55aa")) }, "right click focus")
    compare(JSON.stringify(run.command), JSON.stringify(Launchers.focusWindowCommand("0x55aa")))
  }

  function test_middle_click_refreshes() {
    var widget = start(fixture())
    var before = weekRuns().length
    act.clickWhenReady(widget, { button: Qt.MiddleButton, desc: "bar widget (middle)" })
    act.settled(function () { return weekRuns().length >= before + 1 }, "middle click never refreshed")
    act.drain()
    compare(weekRuns().length, before + 1)
    compare(Quickshell.execDetachedCalls.length, 0)
  }

  function test_refreshes_every_interval() {
    var widget = start(fixture())
    widget.refreshIntervalMs = 30
    var before = weekRuns().length
    tryVerify(function () { return weekRuns().length > before }, 2000)
  }
}
