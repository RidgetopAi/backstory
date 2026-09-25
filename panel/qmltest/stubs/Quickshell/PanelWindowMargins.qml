import QtQuick

// See PanelWindow.qml's own doc comment: grouped-property syntax needs its
// target property's STATIC type to expose those sub-properties at compile
// time, which a bare inline `QtObject { property real top... }` default
// value does not provide.
QtObject {
  property real top: 0
  property real bottom: 0
  property real left: 0
  property real right: 0
}
