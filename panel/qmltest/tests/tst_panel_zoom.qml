import QtQuick
import QtTest
import Quickshell
import Quickshell.Io
import "testutil.js" as TestUtil
import "../../js/launchers.js" as Launchers

// The card fills the window and its content reflows with the window size,
// as a web page does: a wider window lays the view out wider at the same
// text size. Ctrl+= / Ctrl+- / Ctrl+0 set the zoom within [0.75, 2.5].
TestCase {
  id: testCase
  name: "PanelZoom"
  when: windowShown
  visible: true
  width: 900
  height: 900

  Component {
    id: panelComponent
    Loader { source: Qt.resolvedUrl("../../Panel.qml") }
  }

  property var loader: null
  property var panel: null

  TestActions { id: act; testCase: testCase }

  function init() {
    ProcessController.reset()
    Quickshell.reset()
    loader = panelComponent.createObject(testCase)
    tryCompare(loader, "status", Loader.Ready, 2000)
    panel = loader.item
    panel.testContentItem.parent = loader
    ProcessController.respond(Launchers.thisWeekCommand(), { stdout: "{}", stderr: "", exitCode: 0 })
    panel.open("{}")
    tryCompare(panel.view, "loading", false, 2000)
    panel.testWindow.width = panel.baseWidth
    panel.testWindow.height = 640
    act.settledEq(panel, "zoom", 1.0)
    act.drain()
  }

  function cleanup() {
    loader.destroy()
  }

  function heading() {
    return TestUtil.findFirst(panel.testContentItem, function (n) {
      return n.text === "Backstory" && n.font !== undefined
    })
  }

  function onScreenHeight(item) {
    var a = item.mapToItem(null, 0, 0)
    var b = item.mapToItem(null, item.width, item.height)
    return b.y - a.y
  }

  function press(key, mods) {
    panel.testKeyItem.forceActiveFocus()
    act.pressKey(key, mods)
  }

  function test_window_width_reflows_content() {
    var h = heading()
    verify(h !== null, "heading Text not found")
    var base = onScreenHeight(h)
    verify(base > 0)

    var win = panel.testWindow
    win.width = 2 * panel.baseWidth
    win.height = 900
    var card = panel.testKeyItem
    act.settledEq(card, "width", win.width)
    act.settledEq(card, "height", win.height)
    act.settledEq(panel, "zoom", 1.0)
    act.settledEq(panel.view, "width", win.width)
    act.settled(function () { return Math.abs(onScreenHeight(h) - base) <= 1 }, "heading " + onScreenHeight(h) + " vs " + base)
  }

  function test_zoom_keeps_content_filling_the_window() {
    var win = panel.testWindow
    win.width = 2 * panel.baseWidth
    press(Qt.Key_Equal, Qt.ControlModifier)
    act.settledEq(panel, "zoom", 1.1)
    var view = panel.view
    act.settled(function () { return Math.abs(view.width * view.scale - win.width) <= 1 }, "view " + view.width * view.scale + " vs window " + win.width)
  }

  function test_zoom_keys() {
    press(Qt.Key_Equal, Qt.ControlModifier)
    act.settledEq(panel, "userZoom", 1.1)
    press(Qt.Key_Plus, Qt.ControlModifier | Qt.ShiftModifier)
    act.settledEq(panel, "userZoom", 1.2)
    press(Qt.Key_Minus, Qt.ControlModifier)
    act.settledEq(panel, "userZoom", 1.1)
    act.settledEq(panel, "zoom", 1.1)
    press(Qt.Key_0, Qt.ControlModifier)
    act.settledEq(panel, "userZoom", 1.0)
  }

  function test_zoom_clamps() {
    for (var i = 0; i < 30; i++) press(Qt.Key_Equal, Qt.ControlModifier)
    act.settledEq(panel, "userZoom", 2.5)
    for (i = 0; i < 40; i++) press(Qt.Key_Minus, Qt.ControlModifier)
    act.settledEq(panel, "userZoom", 0.75)
  }

  function test_plain_keys_leave_zoom_alone() {
    press(Qt.Key_Equal)
    press(Qt.Key_Minus)
    press(Qt.Key_0)
    act.drain()
    act.settledEq(panel, "userZoom", 1.0)
  }

  function test_existing_keys_still_work() {
    press(Qt.Key_J)
    press(Qt.Key_K)
    act.drain()
    act.settledEq(panel.view, "selectedIndex", 0)
    press(Qt.Key_Escape)
    act.settledEq(panel, "opened", false)
  }
}
