import QtQuick
import "js/scroll.js" as Scroll

// A view's page: its header (the hero row, and the rule under it in a
// sub-view) stays put while the rest scrolls under it, as the footer does
// below. `spacing` separates the header's items, the header from the rest,
// and the rest's items. implicitHeight is everything unscrolled, so a host
// sizes to the content until it runs out of room.
Item {
  id: root

  property real spacing: 0
  property alias header: headerColumn.data
  default property alias content: body.data
  readonly property alias flickable: flick

  implicitHeight: headerColumn.implicitHeight + root.spacing + body.implicitHeight

  function reveal(target) { Scroll.reveal(flick, target) }
  function scrollBy(dy) { Scroll.scrollBy(flick, dy) }
  function scrollToTop() { flick.contentY = 0 }

  Column {
    id: headerColumn
    width: parent.width
    spacing: root.spacing
  }

  Flickable {
    id: flick
    anchors.top: headerColumn.bottom
    anchors.topMargin: root.spacing
    anchors.left: parent.left
    anchors.right: parent.right
    anchors.bottom: parent.bottom
    contentWidth: width
    contentHeight: body.implicitHeight
    clip: true

    Column {
      id: body
      width: flick.width
      spacing: root.spacing
    }
  }
}
