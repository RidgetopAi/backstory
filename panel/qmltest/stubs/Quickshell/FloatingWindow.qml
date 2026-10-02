import QtQuick

// Minimal stand-in for Quickshell's FloatingWindow — a regular xdg toplevel
// (real Quickshell backs it with QQuickWindow). Same shape as the
// PanelWindow stub's contentItem hosting, minus the layer-shell-only
// anchors/margins/exclusionMode, which FloatingWindow does not have.
QtObject {
  id: window

  property string title: ""
  property bool visible: false
  property color color: "transparent"
  property real implicitWidth: 0
  property real implicitHeight: 0

  default property list<QtObject> data

  readonly property Item contentItem: _content

  property Item _content: Item {
    x: 0
    y: 0
    width: window.implicitWidth
    height: window.implicitHeight
  }

  Component.onCompleted: {
    for (var i = 0; i < window.data.length; i++) {
      var child = window.data[i]
      if (child && ("parent" in child)) {
        child.parent = window._content
      }
    }
  }
}
