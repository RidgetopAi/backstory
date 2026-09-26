import QtQuick

// Minimal stand-in for qs.Ui.PanelActionButton — an icon-glyph button (see
// qs/Commons/Style.qml's own doc comment for why this stays minimal).
Item {
  id: root

  property string iconText: ""
  property string tooltipText: ""
  property color foreground: "black"
  property color hoverColor: "black"
  property real size: 24
  property real fontSize: 14

  signal clicked()

  implicitWidth: size
  implicitHeight: size

  Text {
    anchors.centerIn: parent
    textFormat: Text.PlainText
    text: root.iconText
    font.pixelSize: root.fontSize
    color: mouse.containsMouse ? root.hoverColor : root.foreground
  }

  MouseArea {
    id: mouse
    objectName: "panelActionButtonMouseArea"
    anchors.fill: parent
    hoverEnabled: true
    enabled: root.enabled
    cursorShape: Qt.PointingHandCursor
    onClicked: root.clicked()
  }
}
