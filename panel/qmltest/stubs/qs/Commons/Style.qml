pragma Singleton
import QtQuick

// Minimal stand-in for the real omarchy-shell qs.Commons.Style singleton —
// only the members panel/*.qml actually references (panel_contract_test.go's
// sibling harness: a reference to a symbol this stub lacks must fail the
// QML load, so this file stays in lockstep with grep -oh 'Style\.[a-zA-Z.()]+'
// over panel/*.qml, not a full reimplementation of the real theme).
//
// Test knobs, scaled as the real Style scales them: fontScale is
// `[font] base-size` / 12; spacing follows it unless spacingScaleWithFont
// is false (`[spacing] scale-with-font`); the bar follows the font.
QtObject {
  id: style

  property real fontScale: 1
  property bool spacingScaleWithFont: true
  readonly property real spacingScale: spacingScaleWithFont ? fontScale : 1

  function fontPx(n) { return Math.max(1, Math.round(n * style.fontScale)) }

  readonly property int cornerRadius: 8

  readonly property QtObject font: QtObject {
    readonly property string family: "sans-serif"
    readonly property int heading: style.fontPx(18)
    readonly property int title: style.fontPx(16)
    readonly property int body: style.fontPx(14)
    readonly property int bodySmall: style.fontPx(13)
    readonly property int caption: style.fontPx(11)
  }

  readonly property QtObject bar: QtObject {
    readonly property int sizeHorizontal: style.fontPx(26)
  }

  readonly property QtObject spacing: QtObject {
    readonly property int panelPadding: style.space(12)
    readonly property int labelGap: style.space(6)
    readonly property int rowGap: style.space(8)
    readonly property int rowPaddingX: style.space(8)
    readonly property int panelGap: style.space(14)
    readonly property int controlGap: style.space(6)
    readonly property int dropdownWidth: style.space(160)
  }

  function space(n) { return Math.max(1, Math.round(n * style.spacingScale)) }

  function hoverFillFor(fg, bg) { return Qt.rgba(fg.r, fg.g, fg.b, 0.12) }
}
