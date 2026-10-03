import QtQuick
import QtTest
import Quickshell
import Quickshell.Io
import "testutil.js" as TestUtil
import "../../js/launchers.js" as Launchers

// Task eb7188fa: the card fills the window and content scales with the
// window width (zoom = width / 380 x userZoom); Ctrl+= / Ctrl+- / Ctrl+0
// adjust userZoom within [0.75, 2.5].
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

  function init() {
    ProcessController.reset()
    Quickshell.reset()
    loader = panelComponent.createObject(testCase)
    tryCompare(loader, "status", Loader.Ready, 2000)
    panel = loader.item
    panel.testContentItem.parent = loader
    ProcessController.respond(Launchers.thisWeekCommand(), { stdout: "{}", stderr: "", exitCode: 0 })
    panel.open("{}")
    tryCompare(panel, "loading", false, 2000)
    panel.testWindow.width = 380
    panel.testWindow.height = 640
    wait(20)
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
    keyClick(key, mods === undefined ? Qt.NoModifier : mods)
  }

  function test_window_width_scales_content() {
    compare(panel.userZoom, 1.0)
    compare(panel.zoom, 1.0)
    var h = heading()
    verify(h !== null, "heading Text not found")
    var base = onScreenHeight(h)
    verify(base > 0)

    var win = panel.testWindow
    win.width = 760
    win.height = 900
    compare(panel.zoom, 2.0)
    var card = panel.testKeyItem
    compare(card.width, win.width)
    compare(card.height, win.height)
    verify(Math.abs(onScreenHeight(h) - 2 * base) <= 1, "heading " + onScreenHeight(h) + " vs 2x " + base)
  }

  function test_zoom_keys() {
    press(Qt.Key_Equal, Qt.ControlModifier)
    compare(panel.userZoom, 1.1)
    press(Qt.Key_Plus, Qt.ControlModifier | Qt.ShiftModifier)
    compare(panel.userZoom, 1.2)
    press(Qt.Key_Minus, Qt.ControlModifier)
    compare(panel.userZoom, 1.1)
    compare(panel.zoom, 1.1)
    press(Qt.Key_0, Qt.ControlModifier)
    compare(panel.userZoom, 1.0)
  }

  function test_zoom_clamps() {
    for (var i = 0; i < 30; i++) press(Qt.Key_Equal, Qt.ControlModifier)
    compare(panel.userZoom, 2.5)
    for (i = 0; i < 40; i++) press(Qt.Key_Minus, Qt.ControlModifier)
    compare(panel.userZoom, 0.75)
  }

  function test_plain_keys_leave_zoom_alone() {
    press(Qt.Key_Equal)
    press(Qt.Key_Minus)
    press(Qt.Key_0)
    compare(panel.userZoom, 1.0)
  }

  function test_existing_keys_still_work() {
    press(Qt.Key_J)
    press(Qt.Key_K)
    compare(panel.selectedIndex, 0)
    press(Qt.Key_Escape)
    compare(panel.opened, false)
  }
}
