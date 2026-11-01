pragma Singleton
import QtQuick

// Minimal stand-in for the real omarchy-shell qs.Commons.Style singleton —
// only the members panel/*.qml actually references (panel_contract_test.go's
// sibling harness: a reference to a symbol this stub lacks must fail the
// QML load, so this file stays in lockstep with grep -oh 'Style\.[a-zA-Z.()]+'
// over panel/*.qml, not a full reimplementation of the real theme).
//
// Test knobs, scaled as the real Style scales them: testFontScale is
// `[font] base-size` / 12; spacing follows it unless testSpacingScaleWithFont
// is false (`[spacing] scale-with-font`); the bar follows the font. These
// four exist only here, never in the real Style, hence the test prefix: panel
// code that reached for one would pass the tests and break on a desktop.
QtObject {
  id: style

  property real testFontScale: 1
  property bool testSpacingScaleWithFont: true
  readonly property real testSpacingScale: testSpacingScaleWithFont ? testFontScale : 1

  function testFontPx(n) { return Math.max(1, Math.round(n * style.testFontScale)) }

  readonly property int cornerRadius: 8

  // The real one returns 0 when Hyprland's animations are off.
  function duration(ms) { return ms }

  readonly property QtObject font: QtObject {
    readonly property string family: "sans-serif"
    readonly property int display: style.testFontPx(24)
    readonly property int heading: style.testFontPx(18)
    readonly property int title: style.testFontPx(16)
    readonly property int body: style.testFontPx(14)
    readonly property int bodySmall: style.testFontPx(13)
    readonly property int caption: style.testFontPx(11)
  }

  readonly property QtObject bar: QtObject {
    readonly property int sizeHorizontal: style.testFontPx(26)
    readonly property int iconSlot: style.testFontPx(27)
    readonly property int iconFont: style.testFontPx(13)
    readonly property int iconCanvas: style.testFontPx(16)
  }

  readonly property QtObject spacing: QtObject {
    readonly property int hairline: style.space(1)
    readonly property int panelPadding: style.space(12)
    readonly property int labelGap: style.space(6)
    readonly property int rowGap: style.space(8)
    readonly property int rowPaddingX: style.space(8)
    readonly property int panelGap: style.space(14)
    readonly property int controlGap: style.space(6)
    readonly property int dropdownWidth: style.space(160)
  }

  function space(n) { return Math.max(1, Math.round(n * style.testSpacingScale)) }
  function spaceReal(n) { return n > 0 ? n * style.testSpacingScale : 0 }

  function hoverFillFor(fg, bg) { return Qt.rgba(fg.r, fg.g, fg.b, 0.12) }
}
