pragma Singleton
import QtQuick

// Minimal stand-in for qs.Ui.Border — only Border.surfaceSpec(...) and
// Border.flat(...), the functions panel/*.qml calls (see qs/Commons/Style.qml's own doc
// comment for why this stays minimal rather than a full reimplementation).
QtObject {
  function surfaceSpec(themeGroup, borderKey, color, width) {
    return { themeGroup: themeGroup, borderKey: borderKey, color: color, width: width }
  }

  function flat(color, width) {
    return { color: color || "transparent", width: width }
  }
}
