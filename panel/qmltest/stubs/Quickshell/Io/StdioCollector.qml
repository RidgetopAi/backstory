import QtQuick

// Minimal stand-in for Quickshell.Io's StdioCollector (see
// ProcessController.qml's own doc comment for why this stays minimal).
QtObject {
  property string text: ""
  signal streamFinished()
}
