import QtQuick
import Quickshell
import Quickshell.Io
import qs.Ui
import qs.Commons
import "js/model.js" as Model
import "js/derive.js" as Derive
import "js/launchers.js" as Launchers
import "js/format.js" as Format
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
//
// It shows the `here` project's short name (the calendar glyph only when
// there is no `here`) and a dot when Attention is non-empty. Left click
// toggles the panel, right click is Continue on `here`, middle click
// refreshes; it refreshes on every click (the panel opening or closing),
// and every refreshIntervalMs.
Item {
  id: root

  property int refreshIntervalMs: 60000
  property var payload: null

  readonly property var hereData: root.payload ? Model.topHere(root.payload) : null
  readonly property var hereProject: root.payload ? Derive.hereSummary(root.payload) : null
  readonly property string hereName: root.hereData ? Model.shortName(Model.hereDisplayName(root.hereData)) : ""
  readonly property bool needsAttention: root.payload ? Model.topAttention(root.payload).length > 0 : false

  implicitWidth: label.implicitWidth + (attentionDot.visible ? attentionDot.width + Style.spacing.controlGap : 0) + Style.space(12)
  implicitHeight: label.implicitHeight + Style.space(6)

  function refresh() {
    continueAction.refreshDefaultAgent()
    dataProcess.running = false
    dataProcess.running = true
  }

  Component.onCompleted: root.refresh()

  Timer {
    interval: root.refreshIntervalMs
    running: true
    repeat: true
    onTriggered: root.refresh()
  }

  ContinueAction { id: continueAction }

  Process {
    id: dataProcess
    command: Launchers.thisWeekCommand()
    stdout: StdioCollector {
      onStreamFinished: {
        try {
          root.payload = JSON.parse(text || "{}")
        } catch (e) {
          root.payload = null
        }
      }
    }
  }

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
    acceptedButtons: Qt.LeftButton | Qt.RightButton | Qt.MiddleButton
    onClicked: (event) => {
      if (event.button === Qt.RightButton) {
        continueAction.continueOn(root.hereProject)
      } else if (event.button === Qt.MiddleButton) {
        root.refresh()
      } else {
        Quickshell.execDetached(Launchers.barToggleCommand())
        root.refresh()
      }
    }
  }

  Row {
    anchors.centerIn: parent
    spacing: Style.spacing.controlGap

    Text {
      id: label
      objectName: "barLabel"
      anchors.verticalCenter: parent.verticalCenter
      textFormat: Text.PlainText
      text: root.hereName !== "" ? root.hereName : Glyphs.week()
      font.family: Style.font.family
      font.pixelSize: Style.font.body
      color: Color.foreground
    }

    Rectangle {
      id: attentionDot
      objectName: "attentionDot"
      visible: root.needsAttention
      anchors.verticalCenter: parent.verticalCenter
      width: Style.space(7)
      height: width
      radius: width / 2
      color: Color.urgent
    }
  }
}
