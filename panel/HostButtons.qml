import QtQuick
import qs.Ui
import qs.Commons as Commons
import "js/glyphs.js" as Glyphs

// The header buttons that depend on where a view is shown, at the right end
// of every view's header, after the view's own: pop-out in the bar popup,
// pin in the window, then close. Pinned, the window follows you to every
// workspace; the button then shows a filled pin in the accent color,
// unpinned an outline pin.
Row {
  id: root

  property bool canPopOut: false
  property bool canPin: false
  property bool pinned: false
  property bool closeShown: false
  signal popOutRequested()
  signal pinRequested()
  signal closeRequested()

  PanelActionButton {
    visible: root.canPopOut
    iconText: Glyphs.popOut()
    tooltipText: "Pop out"
    foreground: Commons.Color.popups.text
    hoverColor: Commons.Color.accent
    onClicked: root.popOutRequested()
  }

  PanelActionButton {
    objectName: "pinButton"
    visible: root.canPin
    iconText: root.pinned ? Glyphs.pin() : Glyphs.pinOutline()
    tooltipText: root.pinned ? "Unpin: stay on this workspace" : "Pin: follow me to every workspace"
    foreground: root.pinned ? Commons.Color.accent : Commons.Color.popups.text
    hoverColor: Commons.Color.accent
    onClicked: root.pinRequested()
  }

  PanelActionButton {
    objectName: "closeButton"
    visible: root.closeShown
    iconText: Glyphs.close()
    tooltipText: "Close"
    foreground: Commons.Color.popups.text
    hoverColor: Commons.Color.urgent
    onClicked: root.closeRequested()
  }
}
