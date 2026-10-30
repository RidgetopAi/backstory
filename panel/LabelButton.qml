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

  // Held narrower than its natural width, the label fits as qs.Ui Button's
  // does: it spends half the padding, then shrinks to fit, and elides only
  // when even the smallest size does not.
  Text {
    id: text
    anchors.centerIn: parent
    width: Math.max(0, Math.min(implicitWidth, root.width - Style.spacing.rowPaddingX))
    fontSizeMode: Text.HorizontalFit
    minimumPixelSize: Math.round(Style.font.bodySmall * 0.75)
    elide: Text.ElideRight
    horizontalAlignment: Text.AlignHCenter
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
