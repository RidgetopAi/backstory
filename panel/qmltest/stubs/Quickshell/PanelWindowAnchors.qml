import QtQuick

// See PanelWindow.qml's own doc comment: grouped-property syntax needs its
// target property's STATIC type to expose those sub-properties at compile
// time, which a bare inline `QtObject { property bool top... }` default
// value does not provide.
QtObject {
  property bool top: false
  property bool bottom: false
  property bool left: false
  property bool right: false
}
