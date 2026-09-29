import QtQuick
import qs.Commons

// A small text button (PanelActionButton is glyph-only) for MemoryView's
// confirm and scope controls.
Rectangle {
  id: root

  property string label: ""
  property color foreground: Color.foreground
  property color hoverColor: Color.accent

  signal clicked()

  implicitWidth: labelText.implicitWidth + Style.space(16)
  implicitHeight: labelText.implicitHeight + Style.space(8)
  radius: Style.cornerRadius
  border.width: 1
  border.color: mouse.containsMouse ? root.hoverColor : Color.popups.border
  color: "transparent"

  Text {
    id: labelText
    anchors.centerIn: parent
    textFormat: Text.PlainText
    text: root.label
    font.family: Style.font.family
    font.pixelSize: Style.font.caption
    color: mouse.containsMouse ? root.hoverColor : root.foreground
  }

  MouseArea {
    id: mouse
    anchors.fill: parent
    hoverEnabled: true
    cursorShape: Qt.PointingHandCursor
    onClicked: root.clicked()
  }
}
