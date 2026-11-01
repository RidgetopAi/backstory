import QtQuick
import QtTest
import Quickshell
import Quickshell.Io
import "testutil.js" as TestUtil
import "../../js/launchers.js" as Launchers
import "../../js/glyphs.js" as Glyphs

// The "?" help pane (task bcc469a2). The tool list below is a FIXTURE for
// Launchers.toolsCommand(); the pane must render exactly what that response
// carries, so the names here are deliberately not the real tools'.
TestCase {
  id: testCase
  name: "Help"
  when: windowShown
  visible: true
  width: 900
  height: 900

  readonly property var fixtureTools: [
    { name: "fixture_alpha", description: "Alpha does the first fixture thing." },
    { name: "fixture_beta", description: "Beta does the second fixture thing." },
    { name: "fixture_gamma", description: "Gamma does the third fixture thing." }
  ]

  Component {
    id: panelComponent
    Loader { source: Qt.resolvedUrl("../../Panel.qml") }
  }

  property var loader: null
  property var panel: null

  TestActions { id: act; testCase: testCase }

  function summary(key, name) {
    return { project_key: key, display_name: name, cwd: "/home/brian/" + name, agent: "claude", last_active: new Date().toISOString() }
  }

  function init() {
    ProcessController.reset()
    Quickshell.reset()
    ProcessController.respond(Launchers.thisWeekCommand(), {
      stdout: JSON.stringify({
        attention: [], week: [],
        where_left_off: [
          { project: summary("one-key", "one") },
          { project: summary("two-key", "two") }
        ]
      }), stderr: "", exitCode: 0
    })
    ProcessController.respond(Launchers.toolsCommand(), {
      stdout: JSON.stringify({ tools: fixtureTools }), stderr: "", exitCode: 0
    })
    loader = panelComponent.createObject(testCase)
    tryCompare(loader, "status", Loader.Ready, 2000)
    panel = loader.item
    panel.testContentItem.parent = loader
    panel.open("{}")
    tryCompare(panel.view, "loading", false, 2000)
    act.settled(function () { return bodyVisible() }, "panel body never rendered")
  }

  function cleanup() {
    if (loader) loader.destroy()
    loader = null
    panel = null
  }

  function press(key, mods) {
    panel.testKeyItem.forceActiveFocus()
    act.pressKey(key, mods)
  }

  function texts() { return TestUtil.collectVisibleTexts(panel.testContentItem) }

  function bodyVisible() { return texts().indexOf("RECENT") >= 0 }

  function test_question_opens_help_and_hides_body() {
    compare(panel.view.helpOpen, false)
    verify(bodyVisible())
    press(Qt.Key_Question)
    act.settled(function () { return panel.view.helpOpen }, "help never opened")
    act.settled(function () { return !bodyVisible() }, "main body still visible with help open")
  }

  function test_navigation_keys_inert_while_help_open() {
    press(Qt.Key_Question)
    act.settled(function () { return panel.view.helpOpen }, "help never opened")
    var before = panel.view.selectedIndex
    var execs = Quickshell.execDetachedCalls.length
    var runs = ProcessController.runs.length
    press(Qt.Key_J)
    press(Qt.Key_K)
    press(Qt.Key_Down)
    press(Qt.Key_T)
    press(Qt.Key_M)
    act.drain()
    compare(panel.view.selectedIndex, before)
    compare(Quickshell.execDetachedCalls.length, execs)
    compare(ProcessController.runs.length, runs)
    compare(panel.view.memoryOpen, false)
    compare(panel.view.helpOpen, true)
  }

  function test_j_moves_selection_when_help_closed() {
    compare(panel.view.selectedIndex, 0)
    press(Qt.Key_J)
    act.settled(function () { return panel.view.selectedIndex === 1 }, "J never moved the selection")
  }

  function test_escape_closes_panel_from_help() {
    press(Qt.Key_Question)
    press(Qt.Key_Escape)
    act.settled(function () { return panel.opened === false && panel.view.helpOpen === false }, "Escape never closed the panel from help")
  }

  function test_question_again_returns_to_body() {
    press(Qt.Key_Question)
    act.settled(function () { return panel.view.helpOpen }, "help never opened")
    press(Qt.Key_Question)
    act.settled(function () { return !panel.view.helpOpen && bodyVisible() }, "second ? never returned to the body")
  }

  function visibleRules() {
    return TestUtil.findAll(panel.testContentItem, function (n) { return n.visible && n.strength !== undefined })
  }

  function footerText() {
    var n = TestUtil.findFirst(panel.testContentItem, function (node) { return node.objectName === "footerHint" })
    return n === null ? null : n.text
  }

  function test_footer_lists_helps_keys() {
    press(Qt.Key_Question)
    act.settledEq(panel.view, "helpOpen", true)
    act.settled(function () { return footerText() === "j/k scroll · u or ? back · p pin · Esc close" }, "footer never listed Help's keys; got " + footerText())
  }

  function test_j_and_k_scroll_help() {
    press(Qt.Key_Question)
    act.settled(function () { return texts().indexOf("GROUPS") >= 0 }, "help never rendered")
    var area = panel.view.scrollArea
    act.settled(function () { return area.contentHeight > area.height }, "help never grew taller than its view")
    function y(item) { return item.mapToItem(panel.testContentItem, 0, 0).y }
    var title = TestUtil.findFirst(panel.testContentItem, function (n) { return n.text === "Help" })
    var titleY = y(title)
    press(Qt.Key_J)
    act.settled(function () { return area.contentY > 0 }, "j never scrolled down")
    verify(Math.abs(y(title) - titleY) < 0.5, "the header scrolled with the text: " + titleY + " -> " + y(title))
    press(Qt.Key_K)
    act.settledEq(area, "contentY", 0)
  }

  function page(name) {
    return TestUtil.findFirst(panel.testContentItem, function (n) { return n.objectName === name })
  }

  // Opening Help slides the main body out to the left; going back slides
  // Help out to the right. Each records every place it passes through.
  function test_help_slides_the_main_body_out_left_and_goes_back_out_right() {
    var main = page("mainPage")
    var mainSeen = []
    main.offsetChanged.connect(function () { mainSeen.push(main.offset) })
    press(Qt.Key_Question)
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "helpPage") }, "the slide into Help never finished")
    verify(mainSeen.some(function (v) { return v > -1 && v < 0 }), "the main body never slid left: " + mainSeen)
    compare(main.offset, -1)

    var help = page("helpPage")
    var helpSeen = []
    help.offsetChanged.connect(function () { helpSeen.push(help.offset) })
    press(Qt.Key_U)
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "mainPage") }, "the slide back never finished")
    verify(helpSeen.some(function (v) { return v > 0 && v < 1 }), "Help never slid right: " + helpSeen)
    compare(help.offset, 1)
  }

  function test_u_goes_back_to_the_main_view() {
    press(Qt.Key_Question)
    act.settledEq(panel.view, "helpOpen", true)
    press(Qt.Key_U)
    act.settledEq(panel.view, "helpOpen", false)
    act.settled(function () { return texts().indexOf("Backstory") >= 0 }, "main view never came back")
  }

  function test_a_rule_before_each_section() {
    press(Qt.Key_Question)
    act.settled(function () { return texts().indexOf("GROUPS") >= 0 }, "help never rendered")
    act.settled(function () { return TestUtil.slideDone(panel.testContentItem, "helpPage") }, "the slide into Help never finished")
    compare(visibleRules().length, 4)
  }

  function test_help_header_is_a_hero_row() {
    press(Qt.Key_Question)
    act.settled(function () { return texts().indexOf("Help") >= 0 }, "help header never rendered")
    var t = texts()
    var i = t.indexOf("Help")
    compare(t.slice(i - 1, i + 2), [Glyphs.back(), "Help", "Keys, agent tools and groups"])
  }

  function test_help_renders_fixture_tools_and_sections() {
    press(Qt.Key_Question)
    act.launchedNext(function (r) {
      return ProcessController.keyFor(r.command) === ProcessController.keyFor(Launchers.toolsCommand())
    }, "help never ran Launchers.toolsCommand()")
    act.settled(function () { return texts().indexOf(fixtureTools[fixtureTools.length - 1].name) >= 0 }, "tools never rendered")
    var rendered = texts()
    for (var i = 0; i < fixtureTools.length; i++) {
      verify(rendered.indexOf(fixtureTools[i].name) >= 0, "missing tool name " + fixtureTools[i].name)
      verify(rendered.indexOf(fixtureTools[i].description) >= 0, "missing description of " + fixtureTools[i].name)
    }
    verify(rendered.indexOf("MEMORY NAVIGATION") >= 0)
    verify(rendered.indexOf("HOW TO USE") >= 0)
    verify(rendered.indexOf("GROUPS") >= 0)
    var groups = rendered.filter(function (t) { return t.indexOf("backstory group set") >= 0 && t.indexOf("gear") >= 0 })
    verify(groups.length === 1, "Groups section must name the gear and `backstory group set`")
  }

  function test_footer_hint_lists_help() {
    verify(panel.view.footerHint.indexOf("? help") >= 0)
    verify(texts().filter(function (t) { return t.indexOf("? help") >= 0 }).length >= 1)
  }

  function test_layout_within_bounds_help_open() {
    press(Qt.Key_Question)
    var bad = []
    act.settled(function () {
      bad = TestUtil.collectOutOfBounds(panel.testContentItem, panel.testContentItem,
        panel.testImplicitWidth, panel.testImplicitHeight)
      return panel.view.helpOpen && bad.length === 0
    }, "item(s) out of bounds with help open: " + JSON.stringify(bad).slice(0, 2000))
  }

  // The latest view asked for opens: Help set open over Groups or Memory
  // shows alone. No key does that today (keys reach Help only from the main
  // body, and restore() goes back to it first), so set the view's
  // helpOpen, as the host may.
  function test_help_opened_over_groups_or_memory_shows_alone() {
    press(Qt.Key_G)
    act.settledEq(panel.view, "groupEditorOpen", true)
    panel.view.helpOpen = true
    verify(!panel.view.groupEditorOpen, "Groups stayed open under Help")
    panel.view.showMain()
    panel.view.openMemory("one-key", "one", "/home/brian/one")
    act.settledEq(panel.view, "memoryOpen", true)
    panel.view.helpOpen = true
    verify(!panel.view.memoryOpen, "Memory stayed open under Help")
  }

  function test_close_resets_help() {
    press(Qt.Key_Question)
    act.settled(function () { return panel.view.helpOpen }, "help never opened")
    panel.close()
    act.settled(function () { return !panel.view.helpOpen }, "close never reset help")
  }
}
