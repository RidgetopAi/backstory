import QtQuick

// Minimal stand-in for qs.Ui.PanelSectionHeader — a section-label Text (see
// qs/Commons/Style.qml's own doc comment for why this stays minimal).
Text {
  property color foreground: "black"
  textFormat: Text.PlainText
  color: foreground
  font.bold: true
}
