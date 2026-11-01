import QtQuick
import qs.Commons

// One page of ThisWeekView: the main body, or Memory, Groups or Help. It
// waits off to one side (`side`: -1 left, the main body; 1 right, a
// sub-view) and slides to the middle while `current`, back out once it is
// not, so opening a sub-view pushes the main body out to the left and going
// back reverses it. Style.duration is 0 when Hyprland's animations are off.
Item {
  id: page

  property bool current: false
  property int side: 1
  // Where the page is, in page widths: 0 in view, -1 or 1 off to that side.
  property real offset: page.current ? 0 : page.side
  // Off while a snapshot is restored: the view is set, not navigated to.
  property bool animate: true
  Behavior on offset {
    enabled: page.animate
    NumberAnimation { duration: Style.duration(200); easing.type: Easing.OutCubic }
  }

  // Fills the space the pages share; the view in it is as tall as the page,
  // and its own height unscrolled is the page's implicitHeight.
  width: parent ? parent.width : 0
  height: parent ? parent.height : 0
  implicitHeight: children.length > 0 ? children[0].implicitHeight : 0
  x: page.offset * page.width
  visible: Math.abs(page.offset) < 1
}
