import QtQuick
import qs.Commons
import qs.Commons as Commons

// The window's grab handle after a pop-out: a strip along its top with a
// short rounded bar, growing in as the window grows to make room for it.
// Dragging it moves the window with the compositor's own move, as a title
// bar's drag does.
Item {
  id: root

  property bool shown: false
  readonly property real fullHeight: Style.space(18)

  height: root.shown ? root.fullHeight : 0
  visible: root.height > 0
  clip: true
  // Grows in; goes at once (the window is closing).
  Behavior on height {
    enabled: root.shown
    NumberAnimation { duration: Style.duration(450); easing.type: Easing.OutQuint }
  }

  Rectangle {
    objectName: "grabHandleBar"
    anchors.centerIn: parent
    width: Style.space(40)
    height: Style.space(5)
    radius: height / 2
    color: Util.alpha(Commons.Color.popups.text, grab.containsMouse ? 0.7 : 0.4)
  }

  MouseArea {
    id: grab
    anchors.fill: parent
    hoverEnabled: true
    cursorShape: grab.pressed ? Qt.ClosedHandCursor : Qt.OpenHandCursor
    onPressed: root.Window.window.startSystemMove()
  }
}
