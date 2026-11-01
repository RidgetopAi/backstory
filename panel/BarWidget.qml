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

  // Test-only handle onto the popup (its content is built on first open).
  readonly property var testPopup: popup

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
    var scr = popup.screen
    var monX = scr ? scr.x : 0
    var monY = scr ? scr.y : 0
    return {
      x: monX + popup.cardOrigin.x, y: monY + popup.cardOrigin.y,
      w: popup.contentWidth, h: popup.drawnHeight,
      bar: popup.barThickness, gap: popup.gap, monX: monX, monY: monY,
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

  Component.onCompleted: root.refresh()

  // The shortcut opens or closes this popup when the window is not open
  // (ops/hyprland/backstory.lua): the shell routes its own summon and
  // toggle to the window. One bar per monitor; the one on the focused
  // monitor answers. The popup's screen is its bar's (KeyboardPanel takes
  // the bar window's), the monitor cardOnScreen() measures too.
  IpcHandler {
    target: "backstory.this-week.popup"
    enabled: !Hyprland.focusedMonitor || !popup.screen || popup.screen.name === Hyprland.focusedMonitor.name
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

  BarIconButton {
    id: button
    anchors.fill: parent
    bar: root.bar
    text: root.scrollGlyph
    labelText: root.labelText
    slotSize: Style.bar.iconSlot + (root.labelText !== "" ? labelMetrics.advanceWidth + Style.space(4) : 0)
    tooltipText: ""
    onPressed: function(mouseButton) {
      if (mouseButton === Qt.RightButton) {
        continueAction.continueOn(root.hereProject)
      } else if (mouseButton === Qt.MiddleButton) {
        root.refresh()
      } else if (root.windowOpen()) {
        Quickshell.execDetached(Launchers.windowFocusCommand())
      } else {
        root.toggle()
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

  KeyboardPanel {
    id: popup
    anchorItem: button
    owner: root
    bar: root.bar
    open: root.opened
    focusTarget: popup.loadedContent
    contentWidth: popup.fittedContentWidth(Style.space(380))
    contentHeight: popup.fittedContentHeight(popup.loadedContentHeight, Style.space(640))

    content: Item {
      implicitHeight: view.implicitHeight
      focus: true

      // Tab and Shift+Tab move to the next bar panel; the view takes the rest.
      function handleKey(key, modifiers) {
        if (key === Qt.Key_Tab || key === Qt.Key_Backtab) {
          root.switchPanel(key === Qt.Key_Backtab || (modifiers & Qt.ShiftModifier) ? -1 : 1)
          return true
        }
        return view.handleKey(key, modifiers)
      }

      Keys.onPressed: (event) => { event.accepted = handleKey(event.key, event.modifiers) }

      ThisWeekView {
        id: view
        anchors.fill: parent
        edge: 0
        footerBottomMargin: 0
        active: root.opened
        canPopOut: true
        onCloseRequested: root.close()
        onPopOutRequested: root.popOut(view.snapshot())
      }
    }
  }
}
