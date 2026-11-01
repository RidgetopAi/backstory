import QtQuick
import qs.Commons

// Minimal stand-in for qs.Ui.BarIconButton — the host's bar glyph button
// (a WidgetButton with an optional label). Only the surface BarWidget.qml
// touches: glyph, label, slot size and the pressed(button) signal.
Item {
  id: root

  property var bar: null
  property string text: ""
  property string labelText: ""
  property string tooltipText: ""
  property real slotSize: Style.bar.iconSlot

  signal pressed(int button)
  signal wheelMoved(int delta)

  implicitWidth: slotSize
  implicitHeight: Style.bar.sizeHorizontal

  Row {
    anchors.centerIn: parent
    spacing: Style.space(4)

    Text {
      objectName: "barGlyph"
      textFormat: Text.PlainText
      text: root.text
      font.pixelSize: Style.bar.iconFont
    }

    Text {
      objectName: "barLabel"
      visible: root.labelText !== ""
      textFormat: Text.PlainText
      text: root.labelText
      font.pixelSize: Style.bar.iconFont
    }
  }

  MouseArea {
    anchors.fill: parent
    acceptedButtons: Qt.LeftButton | Qt.RightButton | Qt.MiddleButton
    onPressed: function(mouse) { root.pressed(mouse.button) }
    onWheel: function(wheel) { root.wheelMoved(wheel.angleDelta.y) }
  }
}
