import QtQuick
import qs.Ui
import qs.Commons
import "js/glyphs.js" as Glyphs

// The "bar-widget" entry point (manifest.json "kinds": ["panel",
// "bar-widget"], round 2 defect B: "Nothing on screen opens the panel; ...
// add a bar widget ... whose click toggles the panel"). A single Nerd Font
// glyph placed in the bar; clicking it toggles PanelState.opened, the same
// state Panel.qml's own open()/close()/toggle() (still called directly by
// the host per the "panel" kind contract) read and write, so either
// surface toggling the panel keeps the other in sync (see PanelState.qml).
// This file spawns no process of its own — pure QML/JS state, like every
// file in this plugin except js/launchers.js (task 4fe02e30 DONE WHEN
// clause 2).
Item {
  id: root

  implicitWidth: icon.implicitWidth + Style.space(12)
  implicitHeight: icon.implicitHeight + Style.space(6)

  Rectangle {
    anchors.fill: parent
    radius: Style.cornerRadius
    color: mouse.containsMouse ? Style.hoverFillFor(Color.foreground, Color.foreground) : "transparent"
  }

  MouseArea {
    id: mouse
    anchors.fill: parent
    hoverEnabled: true
    cursorShape: Qt.PointingHandCursor
    onClicked: PanelState.toggle()
  }

  Text {
    id: icon
    anchors.centerIn: parent
    textFormat: Text.PlainText
    text: Glyphs.week()
    font.family: Style.font.family
    font.pixelSize: Style.font.body
    color: PanelState.opened ? Color.accent : Color.foreground
  }
}
