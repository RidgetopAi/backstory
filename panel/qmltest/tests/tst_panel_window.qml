import QtQuick
import QtTest
import Quickshell
import Quickshell.Io

// Task 93f7c6fd: the panel's top-level window is a regular FloatingWindow
// (Hyprland-managed, so move/resize binds work), never a layer-shell
// PanelWindow, and carries the stable class ops/hyprland/backstory.lua
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

  function test_top_level_is_floating_window_with_title() {
    ProcessController.reset()
    Quickshell.reset()
    var loader = panelComponent.createObject(testCase)
    tryCompare(loader, "status", Loader.Ready, 2000)
    var w = loader.item.testWindow
    var desc = String(w)
    verify(desc.indexOf("FloatingWindow") === 0, "top-level window is " + desc)
    verify(desc.indexOf("PanelWindow") < 0, "layer-shell PanelWindow used: " + desc)
    compare(loader.item.windowTitle, "backstory")
    compare(w.title, "backstory")
    loader.destroy()
  }

  function makePanel() {
    ProcessController.reset()
    Quickshell.reset()
    var loader = panelComponent.createObject(testCase)
    tryCompare(loader, "status", Loader.Ready, 2000)
    return loader
  }

  // Task d6360806: a window hidden by the host/compositor (not close())
  // must not leave `opened` stuck true.
  function test_toggle_opens_after_host_hides_window() {
    var loader = makePanel()
    var p = loader.item
    p.open("{}")
    compare(p.testWindow.visible, true)
    p.testWindow.visible = false
    compare(p.opened, false)
    p.toggle()
    compare(p.opened, true)
    compare(p.testWindow.visible, true)
    loader.destroy()
  }

  function test_toggle_and_escape_unchanged() {
    var loader = makePanel()
    var p = loader.item
    p.open("{}")
    p.toggle()
    compare(p.opened, false)
    compare(p.testWindow.visible, false)
    p.toggle()
    compare(p.opened, true)
    p.handleKey(Qt.Key_Escape, 0)
    compare(p.opened, false)
    compare(p.testWindow.visible, false)
    p.toggle()
    compare(p.opened, true)
    compare(p.testWindow.visible, true)
    loader.destroy()
  }
}
