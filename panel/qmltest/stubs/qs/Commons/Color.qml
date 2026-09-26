pragma Singleton
import QtQuick

// Minimal stand-in for qs.Commons.Color — only the members panel/*.qml
// references (see Style.qml's own doc comment for why this stays minimal).
QtObject {
  readonly property color foreground: "#e0e0e0"
  readonly property color accent: "#5fb0ff"
  readonly property color urgent: "#ff5f5f"

  readonly property QtObject popups: QtObject {
    readonly property color background: "#202020"
    readonly property color border: "#3a3a3a"
    readonly property color text: "#e0e0e0"
  }
}
