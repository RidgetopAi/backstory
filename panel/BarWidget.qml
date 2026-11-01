import QtQuick
import Quickshell
import Quickshell.Io
import Quickshell.Hyprland
import qs.Ui
import qs.Ui as Ui
import qs.Commons
import qs.Commons as Commons
import "js/model.js" as Model
import "js/derive.js" as Derive
import "js/launchers.js" as Launchers
import "js/format.js" as Format

// The "bar-widget" entry point (manifest.json "kinds": ["panel",
// "bar-widget"]): the project you are in and an attention dot in the bar,
// and a native Omarchy popup with the same view the window shows. Its root
// is a qs.Ui Panel with a KeyboardPanel card, as Omarchy's own bar panels
// are ($OMARCHY_PATH/shell/plugins/panels/monitor/Panel.qml), so the bar
// closes it when another panel opens, Tab moves to the next bar panel, and
// it closes on Esc or a click outside. The window (Panel.qml) stays the
// plugin's "panel" kind: the shell's summon/toggle open that, since the
// shell routes them to the panel loader for a plugin that is both kinds
// ($OMARCHY_PATH/shell/shell.qml isBarWidgetPanelPlugin). CTRL+SHIFT+B
// reaches this popup through its own IPC target below.
//
// Omarchy 4.0 and 4.5 differ in what the popup and the icon button are made
// of, so what only 4.5 has lives in its own file, probed with
// Qt.createComponent where it is used and never named here: BarPopup.qml
// (KeyboardPanel's `content`) and BarLabeledButton.qml (BarIconButton's
// `labelText`). Where one will not load, the widget falls back to what it
// was before the popup: a left click toggles the window through the shell
// (barToggleCommand), and BarPlainButton.qml draws the label in the glyph's
// text. No version string is read.
//
// It shows the `here` project's short name beside the glyph. Left click
// toggles the popup, right click is Continue on `here`, middle click
// refreshes; it refreshes on every click and every refreshIntervalMs.
Ui.Panel {
  id: root
  moduleName: "backstory.this-week"

  property int refreshIntervalMs: 60000
  property var payload: null

  readonly property var hereData: root.payload ? Model.topHere(root.payload) : null
  readonly property var hereProject: root.payload ? Derive.hereSummary(root.payload) : null
  readonly property string hereName: root.hereData ? Model.shortName(Model.hereDisplayName(root.hereData)) : ""
  readonly property bool needsAttention: root.payload ? Model.topAttention(root.payload).length > 0 : false
  readonly property bool vertical: root.bar ? root.bar.vertical === true : false
  readonly property string labelText: root.vertical ? "" : root.hereName

  // Backstory's glyph: md-script_text, an ancient scroll with writing.
  readonly property string scrollGlyph: String.fromCodePoint(0xF0BC2)

  implicitWidth: button.implicitWidth
  // The bar lines its slots up at their top, so the widget takes the bar's
  // full thickness to sit level with the icons beside it
  // ($OMARCHY_PATH/shell/plugins/bar/README.md, "Custom user modules").
  implicitHeight: root.bar ? root.bar.barSize : Style.bar.sizeHorizontal

  // The popup (BarPopup.qml) where this Omarchy can build it (4.5+), else
  // null and a left click toggles the window.
  readonly property Component popupComponent: Qt.createComponent("BarPopup.qml")
  readonly property bool popupSupported: root.popupComponent.status === Component.Ready
  property var popupHost: null
  readonly property var popup: root.popupHost ? root.popupHost.panel : null

  // Test-only handle onto the popup's KeyboardPanel (its content is built on
  // first open).
  readonly property var testPopup: root.popup

  // The popup's pop-out button: open the window, which you can move and
  // which stays open when other bar panels open, at the snapshot the popup's
  // view took just before (where you were in it) and where the popup's card
  // is on screen (`from`, js/popout.js). The popup stays up until the window
  // sits in its place and closes it (closeAfterPopOut), so nothing blinks
  // between them; if the window never does, it closes after popOutGiveUpMs.
  property int popOutGiveUpMs: 1500

  // One pop-out at a time: a second (p twice) would find the window open
  // but still hidden, and close the popup at once.
  function popOut(snapshot) {
    if (popOutGiveUp.running) return
    snapshot.from = root.cardOnScreen()
    popOutGiveUp.restart()
    Quickshell.execDetached(Launchers.windowSummonCommand(JSON.stringify(snapshot)))
  }

  // The window is open: the popup and the window never show together, so a
  // left click focuses the window instead (Panel.qml focusWindow()).
  function windowOpen() {
    return Hyprland.toplevels.values.some(function (t) { return Launchers.isPanelWindow(t) })
  }

  function closeAfterPopOut() {
    popOutGiveUp.stop()
    root.close()
  }

  // The popup's card on screen (logical px), the bar's thickness and the
  // card's gap below it, and the monitor's origin.
  function cardOnScreen() {
    var card = root.popup
    var scr = card.screen
    var monX = scr ? scr.x : 0
    var monY = scr ? scr.y : 0
    // drawnHeight and barThickness are 4.5.0-rc2+; an earlier popup has the
    // card's own height and the bar's size.
    return {
      x: monX + card.cardOrigin.x, y: monY + card.cardOrigin.y,
      w: card.contentWidth, h: card.drawnHeight !== undefined ? card.drawnHeight : card.contentHeight,
      bar: card.barThickness !== undefined ? card.barThickness : (root.bar ? root.bar.barSize : 0),
      gap: card.gap, monX: monX, monY: monY,
      monW: scr ? scr.width : 0, monH: scr ? scr.height : 0,
      edge: root.bar && root.bar.position ? root.bar.position : "top"
    }
  }

  Timer {
    id: popOutGiveUp
    interval: root.popOutGiveUpMs
    onTriggered: root.close()
  }
  // Closed by hand, the popup is done with its pop-out.
  onOpenedChanged: if (!root.opened) popOutGiveUp.stop()

  function refresh() {
    continueAction.refreshDefaultAgent()
    dataProcess.running = false
    dataProcess.running = true
  }

  Component.onCompleted: {
    if (root.popupSupported) root.popupHost = root.popupComponent.createObject(root, { host: root, anchorItem: button })
    root.refresh()
  }

  // The shortcut opens or closes this popup when the window is not open
  // (ops/hyprland/backstory.lua): the shell routes its own summon and
  // toggle to the window. One bar per monitor; the one on the focused
  // monitor answers. The popup's screen is its bar's (KeyboardPanel takes
  // the bar window's), the monitor cardOnScreen() measures too.
  IpcHandler {
    target: "backstory.this-week.popup"
    // Without the popup there is nothing here to toggle: the target stays
    // disabled and the shortcut's chain goes on to the window.
    enabled: root.popup !== null && (!Hyprland.focusedMonitor || !root.popup.screen || root.popup.screen.name === Hyprland.focusedMonitor.name)
    function toggle(): string {
      root.toggle()
      if (root.opened) root.refresh()
      return "ok"
    }
    function close(): string {
      root.closeAfterPopOut()
      return "ok"
    }
  }

  Timer {
    interval: root.refreshIntervalMs
    running: true
    repeat: true
    onTriggered: root.refresh()
  }

  ContinueAction { id: continueAction }

  Process {
    id: dataProcess
    command: Launchers.thisWeekCommand()
    stdout: StdioCollector {
      onStreamFinished: {
        try {
          root.payload = JSON.parse(text || "{}")
        } catch (e) {
          root.payload = null
        }
      }
    }
  }

  TextMetrics {
    id: labelMetrics
    font.family: root.bar ? root.bar.fontFamily : Style.font.family
    font.pixelSize: Style.bar.iconFont
    text: root.labelText
  }

  // The bar button: BarLabeledButton where BarIconButton has `labelText`
  // (4.5+), BarPlainButton where it has not.
  readonly property Component labeledButtonComponent: Qt.createComponent("BarLabeledButton.qml")
  readonly property Component plainButtonComponent: Qt.createComponent("BarPlainButton.qml")

  Loader {
    id: button
    anchors.fill: parent
    sourceComponent: root.labeledButtonComponent.status === Component.Ready ? root.labeledButtonComponent : root.plainButtonComponent
  }

  // The button's own properties by name: its type is not known here.
  Binding { target: button.item; property: "bar"; value: root.bar }
  Binding { target: button.item; property: "glyph"; value: root.scrollGlyph }
  Binding { target: button.item; property: "label"; value: root.labelText }
  Binding {
    target: button.item
    property: "slotSize"
    value: Style.bar.iconSlot + (root.labelText !== "" ? labelMetrics.advanceWidth + Style.space(4) : 0)
  }

  Connections {
    target: button.item
    function onPressed(mouseButton) {
      if (mouseButton === Qt.RightButton) {
        continueAction.continueOn(root.hereProject)
      } else if (mouseButton === Qt.MiddleButton) {
        root.refresh()
      } else if (root.windowOpen()) {
        Quickshell.execDetached(Launchers.windowFocusCommand())
      } else if (root.popup) {
        root.toggle()
        root.refresh()
      } else {
        Quickshell.execDetached(Launchers.barToggleCommand())
        root.refresh()
      }
    }
  }

  // The problem dot at the icon's upper right; the icon is the right end of
  // the button's centered label-and-icon group.
  Item {
    anchors.verticalCenter: button.verticalCenter
    x: (button.width + width) / 2 - width
    width: Style.bar.iconCanvas + (root.labelText !== "" ? labelMetrics.advanceWidth + Style.space(4) : 0)
    height: Style.bar.iconCanvas

    BorderSurface {
      objectName: "attentionDot"
      visible: root.needsAttention
      width: Style.space(6)
      height: width
      radius: width / 2
      color: Commons.Color.bar.active
      anchors.right: parent.right
      anchors.top: parent.top
      anchors.rightMargin: -Style.space(1)
      borderSpec: Border.flat(Commons.Color.popups.background, Style.spacing.hairline)
    }
  }
}
