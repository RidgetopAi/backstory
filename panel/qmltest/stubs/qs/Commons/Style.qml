pragma Singleton
import QtQuick

// Minimal stand-in for the real omarchy-shell qs.Commons.Style singleton —
// only the members panel/*.qml actually references (panel_contract_test.go's
// sibling harness: a reference to a symbol this stub lacks must fail the
// QML load, so this file stays in lockstep with grep -oh 'Style\.[a-zA-Z.()]+'
// over panel/*.qml, not a full reimplementation of the real theme).
QtObject {
  id: style

  readonly property int cornerRadius: 8

  readonly property QtObject font: QtObject {
    readonly property string family: "sans-serif"
    readonly property int heading: 18
    readonly property int title: 16
    readonly property int body: 14
    readonly property int bodySmall: 13
    readonly property int caption: 11
  }

  readonly property QtObject spacing: QtObject {
    readonly property int panelPadding: 12
    readonly property int labelGap: 6
    readonly property int rowGap: 8
    readonly property int rowPaddingX: 8
    readonly property int panelGap: 14
    readonly property int controlGap: 6
    readonly property int dropdownWidth: 160
  }

  function space(n) { return n }

  function hoverFillFor(fg, bg) { return Qt.rgba(fg.r, fg.g, fg.b, 0.12) }
}
