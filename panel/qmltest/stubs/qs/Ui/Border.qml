pragma Singleton
import QtQuick

// Minimal stand-in for qs.Ui.Border — only Border.surfaceSpec(...), the one
// function panel/Panel.qml calls (see qs/Commons/Style.qml's own doc
// comment for why this stays minimal rather than a full reimplementation).
QtObject {
  function surfaceSpec(themeGroup, borderKey, color, width) {
    return { themeGroup: themeGroup, borderKey: borderKey, color: color, width: width }
  }
}
