import QtQuick
import QtTest
import Quickshell
import Quickshell.Io
import Quickshell.Hyprland
import "../tests"
import "../tests/testutil.js" as TestUtil
import "../../js/launchers.js" as Launchers

// The panel on Omarchy 4.0.4 (task aa64900b). `make panel-qml-test` runs this
// directory against stubs-4.0.4: no qs.Ui.OverlayWindow; KeyboardPanel
// without content/loadedContent/barThickness/drawnHeight; BarIconButton
// without labelText; Style without duration. The panel must load there
// with no warning (QT_FATAL_WARNINGS=1 aborts the run on one) and fall back
// to what it did before the 4.5 popup: a bar click toggles the window.
TestCase {
  id: testCase
  name: "Omarchy40"
  when: windowShown
  visible: true
  width: 300
  height: 100

  readonly property string fixtureUrl: Qt.resolvedUrl("../../testdata/this-week-here.json")

  // What only 4.5 has, loaded only through a probe.
  readonly property var only45: ["PopOutGlow.qml", "BarPopup.qml", "BarLabeledButton.qml"]

  Component { id: loaderComponent; Loader {} }

  TestActions { id: act; testCase: testCase }

  property var loader: null

  function cleanup() {
    if (loader) loader.destroy()
    loader = null
  }

  function fixture() {
    var xhr = new XMLHttpRequest()
    xhr.open("GET", fixtureUrl, false)
    xhr.send()
    return JSON.parse(xhr.responseText.replace(/@NOW/g, new Date().toISOString()).replace(/@D\d/g, "2026-01-01"))
  }

  function load(file) {
    ProcessController.reset()
    Quickshell.reset()
    Hyprland.reset()
    ProcessController.respond(Launchers.thisWeekCommand(), { stdout: JSON.stringify(fixture()), stderr: "", exitCode: 0 })
    ProcessController.respond(Launchers.defaultAgentCommand(), { stdout: "claude\n", stderr: "", exitCode: 0 })
    loader = loaderComponent.createObject(testCase, { source: Qt.resolvedUrl("../../" + file) })
    tryCompare(loader, "status", Loader.Ready, 2000)
    verify(loader.item !== null, file + " failed to load: status=" + loader.status)
    return loader.item
  }

  // The stub tree really is 4.0.4: the 4.5 files do not compile against it.
  // A probe (createComponent) is silent where a Loader would warn.
  function test_the_stub_tree_is_4_0_4_data() {
    return only45.map(function (f) { return { tag: f, file: f } })
  }
  function test_the_stub_tree_is_4_0_4(data) {
    var c = Qt.createComponent(Qt.resolvedUrl("../../" + data.file))
    compare(c.status, Component.Error, data.file + " loaded against the 4.0.4 stubs")
  }

  function test_loads_without_warning_data() {
    return ["Panel.qml", "BarWidget.qml", "ThisWeekView.qml", "SlidePage.qml", "GrabHandle.qml"].map(function (f) { return { tag: f, file: f } })
  }
  function test_loads_without_warning(data) {
    load(data.file)
  }

  function test_panel_opens_and_pops_out_with_no_glow() {
    var panel = load("Panel.qml")
    compare(panel.testGlow, null, "a glow loaded without OverlayWindow")
    panel.open("{}")
    compare(panel.opened, true)
    // The pop-out's steps skip the glow rather than throw (a TypeError is a
    // warning, which QT_FATAL_WARNINGS turns into a failed run).
    panel.startPopOut({ x: 100, y: 40, w: 380, h: 500, bar: 26, gap: 8, monX: 0, monY: 0, monW: 1920, monH: 1080, edge: "top" })
    panel.stopPopOut()
    panel.close()
    compare(panel.opened, false)
  }

  function test_bar_widget_has_no_popup_and_a_click_toggles_the_window() {
    var widget = load("BarWidget.qml")
    act.launchedNext(function (r) { return ProcessController.keyFor(r.command) === ProcessController.keyFor(Launchers.thisWeekCommand()) }, "initial this-week fetch")
    compare(widget.popupSupported, false)
    compare(widget.testPopup, null)
    act.clickWhenReady(widget, { button: Qt.LeftButton, desc: "bar widget", until: function () { return Quickshell.execDetachedCalls.length === 1 } })
    compare(JSON.stringify(Quickshell.execDetachedCalls[0].command), JSON.stringify(Launchers.barToggleCommand()))
    compare(widget.opened, false, "a popup state opened with nothing to show it")
  }

  function test_bar_widget_label_shows_beside_the_glyph() {
    var widget = load("BarWidget.qml")
    act.settled(function () {
      var g = TestUtil.findFirst(widget, function (n) { return n.objectName === "barGlyph" })
      return g !== null && g.text.indexOf("foo") >= 0 && g.text.indexOf(widget.scrollGlyph) >= 0
    }, "the label never showed with the glyph")
  }

  // The shortcut's second step is the popup's IPC target; with no popup it
  // answers nothing, so the chain goes on to the window.
  function test_popup_ipc_target_is_off() {
    var widget = load("BarWidget.qml")
    var ipc = null
    for (var i = 0; i < widget.resources.length; i++)
      if (widget.resources[i].target === "backstory.this-week.popup") ipc = widget.resources[i]
    verify(ipc !== null, "no popup IPC target")
    compare(ipc.enabled, false)
  }

  // The animations' durations come from Style.duration where it exists; on
  // 4.0 it is a missing function, and calling it is a TypeError the moment
  // the animation starts.
  function test_slide_page_and_grab_handle_animate_without_style_duration() {
    var page = load("SlidePage.qml")
    page.current = true
    tryCompare(page, "offset", 0, 2000)
    page.current = false
    tryCompare(page, "offset", 1, 2000)
    loader.destroy()
    var handle = load("GrabHandle.qml")
    handle.shown = true
    tryCompare(handle, "height", handle.fullHeight, 3000)
  }
}
