import QtQuick
import QtTest
import Quickshell
import Quickshell.Io
import "testutil.js" as TestUtil
import "../../js/launchers.js" as Launchers

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
    tryCompare(panel, "loading", false, 2000)
    wait(20)
  }

  function cleanup() {
    if (loader) loader.destroy()
    loader = null
    panel = null
  }

  function press(key, mods) {
    panel.testKeyItem.forceActiveFocus()
    keyClick(key, mods === undefined ? Qt.NoModifier : mods)
  }

  function texts() { return TestUtil.collectVisibleTexts(panel.testContentItem) }

  function bodyVisible() { return texts().indexOf("RECENT") >= 0 }

  function test_question_opens_help_and_hides_body() {
    compare(panel.helpOpen, false)
    verify(bodyVisible())
    press(Qt.Key_Question)
    compare(panel.helpOpen, true)
    verify(!bodyVisible(), "main body still visible with help open")
  }

  function test_navigation_keys_inert_while_help_open() {
    press(Qt.Key_Question)
    compare(panel.helpOpen, true)
    var before = panel.selectedIndex
    var execs = Quickshell.execDetachedCalls.length
    var runs = ProcessController.runs.length
    press(Qt.Key_J)
    press(Qt.Key_K)
    press(Qt.Key_Down)
    press(Qt.Key_T)
    press(Qt.Key_M)
    compare(panel.selectedIndex, before)
    compare(Quickshell.execDetachedCalls.length, execs)
    compare(ProcessController.runs.length, runs)
    compare(panel.memoryOpen, false)
    compare(panel.helpOpen, true)
  }

  function test_j_moves_selection_when_help_closed() {
    compare(panel.selectedIndex, 0)
    press(Qt.Key_J)
    compare(panel.selectedIndex, 1)
  }

  function test_escape_closes_panel_from_help() {
    press(Qt.Key_Question)
    press(Qt.Key_Escape)
    compare(panel.opened, false)
    compare(panel.helpOpen, false)
  }

  function test_question_again_returns_to_body() {
    press(Qt.Key_Question)
    press(Qt.Key_Question)
    compare(panel.helpOpen, false)
    verify(bodyVisible())
  }

  function test_help_renders_fixture_tools_and_sections() {
    press(Qt.Key_Question)
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
    var runs = ProcessController.runs.filter(function (r) {
      return ProcessController.keyFor(r.command) === ProcessController.keyFor(Launchers.toolsCommand())
    })
    verify(runs.length >= 1, "help never ran Launchers.toolsCommand()")
  }

  function test_footer_hint_lists_help() {
    verify(panel.footerHint.indexOf("? help") >= 0)
    verify(texts().filter(function (t) { return t.indexOf("? help") >= 0 }).length >= 1)
  }

  function test_layout_within_bounds_help_open() {
    press(Qt.Key_Question)
    wait(20)
    var bad = TestUtil.collectOutOfBounds(panel.testContentItem, panel.testContentItem,
      panel.testImplicitWidth, panel.testImplicitHeight)
    verify(bad.length === 0, bad.length + " item(s) out of bounds with help open: " + JSON.stringify(bad).slice(0, 2000))
  }

  function test_close_resets_help() {
    press(Qt.Key_Question)
    panel.close()
    compare(panel.helpOpen, false)
  }
}
