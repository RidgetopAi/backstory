import QtQuick
import qs.Ui

// The bar button where BarIconButton has no `labelText` (Omarchy 4.0): the
// label goes in front of the glyph in the one text, which is how the pair
// reads on 4.5 too. BarWidget.qml uses BarLabeledButton.qml where it loads.
BarIconButton {
  property string glyph: ""
  property string label: ""

  text: label !== "" ? label + " " + glyph : glyph
  tooltipText: ""
}
