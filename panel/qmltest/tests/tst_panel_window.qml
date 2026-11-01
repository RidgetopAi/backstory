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

  TestActions { id: act; testCase: testCase }

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
    act.settledEq(p.testWindow, "visible", true)
    p.testWindow.visible = false
    act.settledEq(p, "opened", false)
    p.toggle()
    act.settledEq(p, "opened", true)
    act.settledEq(p.testWindow, "visible", true)
    loader.destroy()
  }

  // Hyprland draws the window's border, corners and shadow, as on any other
  // window; the card adds no border or rounded corners of its own.
  function test_the_card_draws_no_border_or_corners() {
    var loader = makePanel()
    var card = loader.item.testKeyItem
    compare(card.radius, 0)
    compare(card.border.width, 0)
    loader.destroy()
  }

  // The card fades in on every open, as Omarchy's own panels do (PopupCard:
  // opacity 0 -> 1 with a slight grow), instead of popping in or relying on
  // Hyprland's slower window animation. Closing hides it at once.
  function test_card_fades_in_on_every_open() {
    var loader = makePanel()
    var p = loader.item
    var card = p.testKeyItem
    p.open("{}")
    verify(card.opacity < 1, "card fully opaque the moment it opens (opacity " + card.opacity + "): no fade")
    tryCompare(card, "opacity", 1, 1000)
    compare(card.scale, 1)
    p.close()
    compare(card.opacity, 0)
    p.open("{}")
    verify(card.opacity < 1, "second open: card fully opaque at once (opacity " + card.opacity + ")")
    tryCompare(card, "opacity", 1, 1000)
    loader.destroy()
  }

  function test_toggle_and_escape_unchanged() {
    var loader = makePanel()
    var p = loader.item
    p.open("{}")
    p.toggle()
    act.settledEq(p, "opened", false)
    act.settledEq(p.testWindow, "visible", false)
    p.toggle()
    act.settledEq(p, "opened", true)
    p.handleKey(Qt.Key_Escape, 0)
    act.settledEq(p, "opened", false)
    act.settledEq(p.testWindow, "visible", false)
    p.toggle()
    act.settledEq(p, "opened", true)
    act.settledEq(p.testWindow, "visible", true)
    loader.destroy()
  }
}
