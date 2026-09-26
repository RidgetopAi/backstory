import QtQuick

// Minimal stand-in for qs.Ui.BorderSurface — a bordered, filled container
// (see qs/Commons/Style.qml's own doc comment for why this stays minimal).
// A plain Rectangle carries color/radius/width/height/children already;
// borderSpec is the one extra property panel/Panel.qml sets.
Rectangle {
  property var borderSpec: null
}
