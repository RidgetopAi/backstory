import QtQuick

// Minimal stand-in for Quickshell's ExclusionMode enum — Panel.qml only
// ever reads ExclusionMode.Ignore (see Quickshell.qml's own doc comment
// for why this stays minimal). QML enum VALUES may be PascalCase even
// though plain properties may not (Property names cannot begin with an
// upper case letter) — this needs an `enum` block, not `property int`.
QtObject {
  enum Mode { Ignore, Auto, Normal }
}
