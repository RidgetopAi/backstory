import QtQuick

// Minimal stand-in for qs.Ui.PanelSeparator — a one-pixel rule.
Rectangle {
  property color foreground: "black"
  property real strength: 0.12

  implicitHeight: 1
  color: Qt.rgba(foreground.r, foreground.g, foreground.b, strength)
}
