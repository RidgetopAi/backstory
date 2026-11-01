import QtQuick

// Minimal stand-in for qs.Ui.KeyboardPanel, a layer-shell window in the real
// shell (so, like PanelWindow.qml here, a QtObject outside the test scene).
// Its content is built on first open and kept, as the real one's is.
QtObject {
  id: root

  property Item anchorItem: null
  property QtObject bar: null
  property var owner: null
  property bool open: false
  property Item focusTarget: null
  property int contentWidth: 0
  property int contentHeight: 0
  property Component content: null
  property bool contentRequested: false
  // Where the card sits on screen, as the real one computes it.
  property point cardOrigin: Qt.point(0, 0)
  property real drawnHeight: contentHeight
  property real barThickness: 0
  property int gap: 0
  property var screen: null
  // Tests only: the scene the content shows in (the real one's is its own
  // layer window), so it can take focus and real key events.
  property Item testScene: null

  readonly property Item loadedContent: _loader.item
  readonly property real loadedContentHeight: loadedContent ? loadedContent.implicitHeight : 0

  // As the real one: on open, the focus target takes focus on the next turn.
  onOpenChanged: {
    if (!open) return
    if (content) contentRequested = true
    if (focusTarget) Qt.callLater(function() {
      if (root.open && root.focusTarget) root.focusTarget.forceActiveFocus()
    })
  }

  function fittedContentWidth(width, cap) {
    return cap !== undefined && cap > 0 ? Math.min(width, cap) : width
  }

  function fittedContentHeight(implicitHeight, cap) {
    return cap !== undefined && cap > 0 ? Math.min(implicitHeight, cap) : implicitHeight
  }

  property Loader _loader: Loader {
    active: root.content !== null && root.contentRequested
    sourceComponent: root.content
    parent: root.testScene
    width: root.contentWidth
    height: root.contentHeight
  }
}
