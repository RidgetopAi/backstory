import QtQuick
import qs.Ui
import qs.Commons
import qs.Commons as Commons
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
  // Picked for the group form below (a check mark), and highlighted by the
  // keys or the mouse (the main view's selected-row look).
  property bool selected: false
  property bool current: false
  property bool removable: false
  property real indent: 0

  signal picked()
  // Each pointer move over the row, for the editor's PointerMoveGate.
  signal hovered(var item, var mouse)
  signal removeRequested()

  implicitWidth: parent ? parent.width : Style.space(320)
  implicitHeight: Style.space(28)

  // Drawn like the main view's selected row (ProjectRow.qml).
  Rectangle {
    objectName: "groupRowBackground"
    anchors.fill: parent
    radius: Style.cornerRadius
    color: root.current ? Util.alpha(Commons.Color.accent, 0.18) : "transparent"
    border.width: root.current ? 1 : 0
    border.color: Commons.Color.accent
  }

  MouseArea {
    id: rowMouse
    anchors.fill: parent
    anchors.rightMargin: removeButton.visible ? Style.space(24) : 0
    hoverEnabled: true
    cursorShape: Qt.PointingHandCursor
    onPositionChanged: (mouse) => root.hovered(rowMouse, mouse)
    onClicked: root.picked()
  }

  Text {
    id: checkMark
    objectName: "pickedMark"
    visible: root.selected
    textFormat: Text.PlainText
    text: Glyphs.check()
    font.family: Style.font.family
    font.pixelSize: Style.font.caption
    color: Commons.Color.accent
    anchors.verticalCenter: parent.verticalCenter
    anchors.left: parent.left
    anchors.leftMargin: root.indent + Style.spacing.rowPaddingX
  }

  Text {
    textFormat: Text.PlainText
    text: root.displayName
    font.family: Style.font.family
    font.pixelSize: Style.font.bodySmall
    font.bold: root.selected
    color: Commons.Color.foreground
    elide: Text.ElideRight
    anchors.verticalCenter: parent.verticalCenter
    anchors.left: parent.left
    anchors.leftMargin: root.indent + Style.spacing.rowPaddingX + (root.selected ? checkMark.implicitWidth + Style.spacing.controlGap : 0)
    anchors.right: removeButton.visible ? removeButton.left : parent.right
  }

  PanelActionButton {
    id: removeButton
    visible: root.removable
    anchors.right: parent.right
    anchors.verticalCenter: parent.verticalCenter
    iconText: Glyphs.close()
    tooltipText: "Remove from group"
    foreground: Commons.Color.foreground
    hoverColor: Commons.Color.urgent
    size: Style.space(16)
    fontSize: Style.font.caption
    onClicked: root.removeRequested()
  }
}
