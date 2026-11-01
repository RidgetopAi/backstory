import QtQuick
import qs.Ui
import qs.Commons
import qs.Commons as Commons
import "js/glyphs.js" as Glyphs

// A view's hero row, as Omarchy's own panels draw it (Ui/PanelHero.qml): a
// glyph at Style.font.display, a bold title and a dim uppercased status line,
// with the view's buttons (this item's children) at the trailing edge. A
// sub-view leads with a back button (showBack) in place of the glyph, as a
// page you came into does.
Item {
  id: root

  signal back()

  property bool showBack: false
  property string glyph: ""
  property string title: ""
  property string status: ""
  property bool statusShown: true
  property bool statusUrgent: false
  default property alias buttons: trailing.data

  width: parent ? parent.width : implicitWidth
  height: Math.max(hero.implicitHeight, trailing.implicitHeight)

  Row {
    id: hero
    anchors.left: parent.left
    anchors.verticalCenter: parent.verticalCenter
    spacing: Style.space(14)

    PanelActionButton {
      visible: root.showBack
      iconText: Glyphs.back()
      tooltipText: "Back"
      foreground: Commons.Color.popups.text
      hoverColor: Commons.Color.accent
      anchors.verticalCenter: parent.verticalCenter
      onClicked: root.back()
    }

    Text {
      visible: root.glyph !== ""
      textFormat: Text.PlainText
      text: root.glyph
      font.family: Style.font.family
      font.pixelSize: Style.font.display
      color: Commons.Color.popups.text
      anchors.verticalCenter: parent.verticalCenter
    }

    Column {
      id: labels
      anchors.verticalCenter: parent.verticalCenter
      spacing: Style.space(2)
      // Up to the buttons: a long title or status is cut with an ellipsis.
      readonly property real room: Math.max(0, root.width - trailing.width - labels.x - Style.space(8))

      Text {
        objectName: "heroTitle"
        width: Math.min(implicitWidth, labels.room)
        elide: Text.ElideRight
        textFormat: Text.PlainText
        text: root.title
        font.family: Style.font.family
        font.pixelSize: Style.font.title
        font.bold: true
        color: Commons.Color.popups.text
      }
      Text {
        objectName: "heroStatus"
        width: Math.min(implicitWidth, labels.room)
        elide: Text.ElideRight
        textFormat: Text.PlainText
        text: root.status
        visible: root.statusShown && root.status !== ""
        font.family: Style.font.family
        font.pixelSize: Style.font.caption
        font.bold: true
        font.letterSpacing: 1.2
        font.capitalization: Font.AllUppercase
        color: root.statusUrgent ? Commons.Color.urgent : Qt.darker(Commons.Color.popups.text, 1.4)
      }
    }
  }

  Row {
    id: trailing
    anchors.right: parent.right
    anchors.verticalCenter: parent.verticalCenter
  }
}
