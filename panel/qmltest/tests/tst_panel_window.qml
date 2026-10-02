import QtQuick
import QtTest
import Quickshell
import Quickshell.Io

// Task 93f7c6fd: the panel's top-level window is a regular FloatingWindow
// (Hyprland-managed, so move/resize binds work), never a layer-shell
// PanelWindow, and carries the stable class ops/hyprland/backstory.conf
// matches.
TestCase {
  id: testCase
  name: "PanelWindow"
  when: windowShown
  visible: true
  width: 400
  height: 400

  Component {
    id: panelComponent
    Loader { source: Qt.resolvedUrl("../../Panel.qml") }
  }

  function test_top_level_is_floating_window_with_class() {
    ProcessController.reset()
    Quickshell.reset()
    var loader = panelComponent.createObject(testCase)
    tryCompare(loader, "status", Loader.Ready, 2000)
    var w = loader.item.testWindow
    var desc = String(w)
    verify(desc.indexOf("FloatingWindow") === 0, "top-level window is " + desc)
    verify(desc.indexOf("PanelWindow") < 0, "layer-shell PanelWindow used: " + desc)
    compare(loader.item.windowClass, "backstory")
    compare(w.title, "backstory")
    loader.destroy()
  }
}
