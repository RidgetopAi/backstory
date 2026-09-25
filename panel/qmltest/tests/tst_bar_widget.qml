import QtQuick
import QtTest
import Quickshell
import "../../js/launchers.js" as Launchers

// BarWidget.qml's click (task 4fe02e30 round 4 desk defect A, DONE WHEN
// clause 4): asserts the exact recorded argv is
// ["omarchy-shell","shell","toggle","backstory.this-week","{}"], via
// js/launchers.js's barToggleCommand() — the same MEASURED FIX
// ridgetopai.omarcade's own Marquee.qml:253 uses.
TestCase {
  id: testCase
  name: "BarWidget"
  when: windowShown
  visible: true
  width: 200
  height: 100

  Component {
    id: barWidgetComponent
    Loader { source: Qt.resolvedUrl("../../BarWidget.qml") }
  }

  property var loader: null

  function init() {
    Quickshell.reset()
    loader = barWidgetComponent.createObject(testCase)
    tryCompare(loader, "status", Loader.Ready, 2000)
    verify(loader.item !== null, "BarWidget.qml failed to load: status=" + loader.status)
  }

  function cleanup() {
    if (loader) loader.destroy()
    loader = null
  }

  function test_click_toggles_via_omarchy_shell() {
    var widget = loader.item
    mouseClick(widget, widget.width / 2, widget.height / 2)

    var last = Quickshell.lastCall()
    verify(last !== null, "expected a Quickshell.execDetached() call")
    compare(JSON.stringify(last.command), JSON.stringify(Launchers.barToggleCommand()))
    compare(JSON.stringify(last.command), JSON.stringify(["omarchy-shell", "shell", "toggle", "backstory.this-week", "{}"]))
  }
}
