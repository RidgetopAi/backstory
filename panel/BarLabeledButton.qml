import QtQuick
import qs.Ui

// The bar button where BarIconButton has `labelText` (Omarchy 4.5+): the
// glyph with the label beside it, laid out by the bar. BarWidget.qml probes
// this file and, where it will not load, uses BarPlainButton.qml.
BarIconButton {
  property string glyph: ""
  property string label: ""

  text: glyph
  labelText: label
  tooltipText: ""
}
