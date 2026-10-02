import QtQuick
import qs.Commons

// A small labelled button: the text is the label, `primary` fills it with
// the accent colour.
Rectangle {
  id: root

  property string label: ""
  property bool primary: false

  signal clicked()

  implicitWidth: text.implicitWidth + Style.spacing.rowPaddingX * 2
  implicitHeight: text.implicitHeight + Style.spacing.controlGap * 2
  radius: Style.cornerRadius
  color: root.primary
    ? (mouse.containsMouse ? Qt.lighter(Color.accent, 1.1) : Color.accent)
    : (mouse.containsMouse ? Style.hoverFillFor(Color.foreground, Color.foreground) : "transparent")
  border.width: root.primary ? 0 : 1
  border.color: Color.popups.border

  Text {
    id: text
    anchors.centerIn: parent
    textFormat: Text.PlainText
    text: root.label
    font.family: Style.font.family
    font.pixelSize: Style.font.bodySmall
    font.bold: root.primary
    color: root.primary ? Color.popups.background : Color.popups.text
  }

  MouseArea {
    id: mouse
    anchors.fill: parent
    hoverEnabled: true
    cursorShape: Qt.PointingHandCursor
    onClicked: root.clicked()
  }
}
