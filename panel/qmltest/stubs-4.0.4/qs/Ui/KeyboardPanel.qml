import QtQuick

// qs.Ui.KeyboardPanel as Omarchy 4.0.4 has it: the card's anchor, owner,
// size and open state, and none of 4.5's `content`, `loadedContent`,
// `loadedContentHeight`, `drawnHeight` or `barThickness`. A file that sets one
// of them fails to load against this tree, as it does on a 4.0.4 desk.
QtObject {
  id: root

  property Item anchorItem: null
  property QtObject bar: null
  property var owner: null
  property bool open: false
  property Item focusTarget: null
  property int contentWidth: 0
  property int contentHeight: 0
  property point cardOrigin: Qt.point(0, 0)
  property int gap: 0
  property var screen: null

  function fittedContentWidth(width, cap) {
    return cap !== undefined && cap > 0 ? Math.min(width, cap) : width
  }

  function fittedContentHeight(implicitHeight, cap) {
    return cap !== undefined && cap > 0 ? Math.min(implicitHeight, cap) : implicitHeight
  }
}
