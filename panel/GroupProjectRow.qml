import QtQuick
import qs.Ui
import qs.Commons
import "js/glyphs.js" as Glyphs

// One project row inside GroupEditor.qml — a group's member, or an
// ungrouped project — clickable so choosing a project is never typing its
// raw key (round 2 defect C: "each project ... is a clickable row showing
// its display name, never a key the human types"). GroupEditor.qml looks
// up the display text (js/groups.js's displayNameForKey, against `group
// list --json`'s `projects` array) and passes it in as displayName; this
// file never derives a name from projectKey's own shape (round 5 desk
// defect: a git key's text after the last "/" is "<repo>.git").
Item {
  id: root

  property string projectKey: ""
  property string displayName: projectKey
  property bool selected: false
  property bool removable: false
  property real indent: 0

  signal picked()
  signal removeRequested()

  implicitWidth: parent ? parent.width : Style.space(320)
  implicitHeight: Style.space(28)

  Rectangle {
    anchors.fill: parent
    radius: Style.cornerRadius
    color: root.selected
      ? Util.alpha(Color.accent, 0.25)
      : (rowMouse.containsMouse ? Style.hoverFillFor(Color.foreground, Color.foreground) : "transparent")
  }

  MouseArea {
    id: rowMouse
    anchors.fill: parent
    anchors.rightMargin: removeButton.visible ? Style.space(24) : 0
    hoverEnabled: true
    cursorShape: Qt.PointingHandCursor
    onClicked: root.picked()
  }

  Text {
    textFormat: Text.PlainText
    text: root.displayName
    font.family: Style.font.family
    font.pixelSize: Style.font.bodySmall
    font.bold: root.selected
    color: Color.foreground
    elide: Text.ElideRight
    anchors.verticalCenter: parent.verticalCenter
    anchors.left: parent.left
    anchors.leftMargin: root.indent + Style.spacing.rowPaddingX
    anchors.right: removeButton.visible ? removeButton.left : parent.right
  }

  PanelActionButton {
    id: removeButton
    visible: root.removable
    anchors.right: parent.right
    anchors.verticalCenter: parent.verticalCenter
    iconText: Glyphs.close()
    tooltipText: "Remove from group"
    foreground: Color.foreground
    hoverColor: Color.urgent
    size: Style.space(16)
    fontSize: Style.font.caption
    onClicked: root.removeRequested()
  }
}
