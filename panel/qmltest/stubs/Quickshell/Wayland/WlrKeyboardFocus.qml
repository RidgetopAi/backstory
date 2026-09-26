import QtQuick

// Minimal stand-in for Quickshell.Wayland's WlrKeyboardFocus enum —
// Panel.qml only ever reads WlrKeyboardFocus.OnDemand (see
// Quickshell/PanelWindow.qml's own doc comment for why this module has no
// WlrLayershell attached type; see Quickshell/ExclusionMode.qml's own doc
// comment for why this is an `enum` block, not `property int`).
QtObject {
  enum Focus { None, Exclusive, OnDemand }
}
