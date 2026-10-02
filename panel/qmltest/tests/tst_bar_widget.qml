import QtQuick
import QtTest
import Quickshell
import Quickshell.Io
import "testutil.js" as TestUtil
import "../../js/launchers.js" as Launchers

// BarWidget.qml: shows the `here` project's short name (glyph only when
// there is none) and an attention dot iff Attention is non-empty. Left
// click issues the toggle argv (task 4fe02e30 round 4 desk defect A, via
// js/launchers.js's barToggleCommand(), the same MEASURED FIX
// ridgetopai.omarcade's own Marquee.qml:253 uses), right click is Continue
// on `here`, middle click a refresh (task fc1f340d).
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
    ProcessController.respond(Launchers.thisWeekCommand(), { stdout: JSON.stringify(data), stderr: "", exitCode: 0 })
    ProcessController.respond(Launchers.defaultAgentCommand(), { stdout: defaultAgent === undefined ? "claude\n" : defaultAgent, stderr: "", exitCode: 0 })
    loader = barWidgetComponent.createObject(testCase)
    tryCompare(loader, "status", Loader.Ready, 2000)
    verify(loader.item !== null, "BarWidget.qml failed to load: status=" + loader.status)
    wait(20)
    return loader.item
  }

  function cleanup() {
    if (loader) loader.destroy()
    loader = null
  }

  function weekRuns() {
    return ProcessController.runs.filter(function (r) { return r.command[0] === "backstory" })
  }

  function dot(widget) {
    return TestUtil.findFirst(widget, function (n) { return n.objectName === "attentionDot" })
  }

  function test_shows_here_project_name() {
    var widget = start(fixture())
    var label = TestUtil.findFirst(widget, function (n) { return n.objectName === "barLabel" })
    compare(label.text, "foo")
  }

  function test_attention_dot_iff_attention_non_empty() {
    var widget = start(fixture())
    verify(dot(widget) !== null, "dot should be visible with attention")
    cleanup()
    widget = start(fixture(function (d) { d.attention = [] }))
    compare(dot(widget), null)
  }

  function test_glyph_only_when_no_here() {
    var widget = start(fixture(function (d) { delete d.here }))
    var label = TestUtil.findFirst(widget, function (n) { return n.objectName === "barLabel" })
    verify(label.text !== "foo" && label.text !== "")
  }

  function test_left_click_toggles_via_omarchy_shell() {
    var widget = start(fixture())
    mouseClick(widget, widget.width / 2, widget.height / 2, Qt.LeftButton)

    var last = Quickshell.lastCall()
    verify(last !== null, "expected a Quickshell.execDetached() call")
    compare(JSON.stringify(last.command), JSON.stringify(Launchers.barToggleCommand()))
    compare(JSON.stringify(last.command), JSON.stringify(["omarchy-shell", "shell", "toggle", "backstory.this-week", "{}"]))
  }

  function test_label_drops_projects_prefix() {
    var widget = start(fixture())
    var label = TestUtil.findFirst(widget, function (n) { return n.objectName === "barLabel" })
    verify(label !== null, "barLabel missing")
    compare(label.text, "foo")
  }

  function test_right_click_is_continue_on_here() {
    var widget = start(fixture())
    mouseClick(widget, widget.width / 2, widget.height / 2, Qt.RightButton)
    compare(Quickshell.execDetachedCalls.length, 0)
    var run = ProcessController.lastRun()
    compare(JSON.stringify(run.command.slice(0, 3)), JSON.stringify(["omarchy", "agent", "prompt"]))
    verify(run.command[3].indexOf("handoff-0042") >= 0 && run.command[3].indexOf("Wire the bar") >= 0)
    compare(run.workingDirectory, "/home/brian/projects/foo")
  }

  function test_right_click_focuses_the_open_window() {
    var widget = start(fixture(function (d) { d.where_left_off[0].project.window = "0x55aa" }))
    mouseClick(widget, widget.width / 2, widget.height / 2, Qt.RightButton)
    compare(JSON.stringify(ProcessController.lastRun().command), JSON.stringify(Launchers.focusWindowCommand("0x55aa")))
  }

  function test_middle_click_refreshes() {
    var widget = start(fixture())
    var before = weekRuns().length
    mouseClick(widget, widget.width / 2, widget.height / 2, Qt.MiddleButton)
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
