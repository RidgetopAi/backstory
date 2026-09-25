pragma Singleton
import QtQuick

// Minimal stand-in for qs.Commons.Util — only the members panel/*.qml
// references (see Style.qml's own doc comment for why this stays minimal).
QtObject {
  function alpha(c, a) { return Qt.rgba(c.r, c.g, c.b, a) }
}
