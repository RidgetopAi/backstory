import QtQuick
import Quickshell
import qs.Ui
import qs.Commons
import "js/launchers.js" as Launchers
import "js/glyphs.js" as Glyphs

// The "bar-widget" entry point (manifest.json "kinds": ["panel",
// "bar-widget"], round 2 defect B: "Nothing on screen opens the panel; ...
// add a bar widget ... whose click toggles the panel"). A single Nerd Font
// glyph placed in the bar.
//
// Round 4 desk defect A: a click here used to flip a shared PanelState
// singleton directly, but the host only ever shows a plugin panel through
// its OWN toggle machinery — the singleton flip never reached it, so
// clicking the bar glyph did nothing. MEASURED FIX (hand-patched on the
// desk, confirmed working): route the click through the same call the
// host's own bar-widget plugins use — ridgetopai.omarcade's own
// Marquee.qml:253 — `Quickshell.execDetached(["omarchy-shell", "shell",
// "toggle", "backstory.this-week", "{}"])`, named once in js/launchers.js
// (task 4fe02e30 DONE WHEN clause 5's "every command the QML runs comes
// from js/launchers.js"). This file spawns no process of its own beyond
// that one execDetached call — like every file in this plugin except
// js/launchers.js itself.
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
    onClicked: Quickshell.execDetached(Launchers.barToggleCommand())
  }

  Text {
    id: icon
    anchors.centerIn: parent
    textFormat: Text.PlainText
    text: Glyphs.week()
    font.family: Style.font.family
    font.pixelSize: Style.font.body
    color: Color.foreground
  }
}
