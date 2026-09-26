import QtQuick

// Minimal stand-in for Quickshell.Wayland's WlrLayer enum — Panel.qml only
// ever reads WlrLayer.Top (see Quickshell/PanelWindow.qml's own doc
// comment for why this module has no WlrLayershell attached type; see
// Quickshell/ExclusionMode.qml's own doc comment for why this is an
// `enum` block, not `property int`).
QtObject {
  enum Layer { Background, Bottom, Top, Overlay }
}
