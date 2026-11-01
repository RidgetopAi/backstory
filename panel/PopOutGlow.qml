import QtQuick
import QtQuick.Effects
import Quickshell
import Quickshell.Wayland
import qs.Ui
import qs.Commons
import qs.Commons as Commons

// The pop-out's glow-flash: a blurred accent ring around the window, larger
// than it and fading both outward and inward, that travels with it from the
// popup's card to its rest at Hyprland's window animation (Omarchy's
// looknfeel.lua "windows": easeOutQuint, speed 3.79, about 380 ms). It eases
// in, then goes twice as fast. It lights around the card the moment the
// pop-out starts (light()) and holds there until the window moves (play()).
// Hyprland's shadow settings are global, so one
// window's can't flash; this is a click-through overlay instead, as Omarchy's
// OSD is, mapped only while it plays.
OverlayWindow {
  id: root

  shownKeyboardFocus: WlrKeyboardFocus.None
  mask: Region {}
  shown: root.playing

  readonly property real pad: Style.space(30)
  readonly property int moveMs: Style.duration(380)
  // Set by light() or play() and cleared when the glow is done; the
  // animation itself waits for the surface (started).
  property bool playing: false
  property bool started: false
  property bool travels: false
  property rect fromRect: Qt.rect(0, 0, 0, 0)
  property rect toRect: Qt.rect(0, 0, 0, 0)
  // For tests: the ring's box.
  readonly property Item box: glowBox

  // rect: the popup's card, local to the monitor. The glow fades in there.
  function light(rect) {
    root.fromRect = rect
    root.toRect = rect
    root.begin(false)
  }

  // from, to: the window's rects, local to the monitor. The glow travels
  // with the window, brightening if it isn't yet, then goes.
  function play(from, to) {
    root.fromRect = from
    root.toRect = to
    root.begin(true)
  }

  // Either runs once the overlay's surface is ready.
  function begin(travels) {
    root.travels = travels
    root.started = false
    root.playing = true
    if (root.contentRevealed) root.start()
  }

  function start() {
    root.started = true
    if (root.travels) {
      lightAnim.stop()
      anim.restart()
    } else {
      glowBox.x = root.fromRect.x - root.pad
      glowBox.y = root.fromRect.y - root.pad
      glowBox.width = root.fromRect.width + root.pad * 2
      glowBox.height = root.fromRect.height + root.pad * 2
      lightAnim.restart()
    }
  }

  // The window closed mid-flash, or never came.
  function stop() {
    lightAnim.stop()
    anim.stop()
    glowBox.opacity = 0
    root.playing = false
  }

  onContentRevealedChanged: if (root.contentRevealed && root.playing && !root.started) root.start()

  Item {
    id: glowBox
    opacity: 0

    Rectangle {
      id: ring
      anchors.fill: parent
      anchors.margins: root.pad
      color: "transparent"
      border.width: Style.space(5)
      border.color: Commons.Color.accent
      visible: false
    }

    MultiEffect {
      source: ring
      anchors.fill: ring
      autoPaddingEnabled: true
      blurEnabled: true
      blur: 1.0
      blurMax: 48
      brightness: 0.15
    }

    // A faint crisp line on the window's edge, so the glow reads as its edge.
    Rectangle {
      anchors.fill: parent
      anchors.margins: root.pad
      color: "transparent"
      border.width: 1
      border.color: Util.alpha(Commons.Color.accent, 0.6)
    }
  }

  NumberAnimation {
    id: lightAnim
    target: glowBox; property: "opacity"; to: 1; duration: Style.duration(240); easing.type: Easing.InQuad
  }

  ParallelAnimation {
    id: anim
    onFinished: root.playing = false
    NumberAnimation {
      target: glowBox; property: "x"; duration: root.moveMs; easing.type: Easing.OutQuint
      from: root.fromRect.x - root.pad; to: root.toRect.x - root.pad
    }
    NumberAnimation {
      target: glowBox; property: "y"; duration: root.moveMs; easing.type: Easing.OutQuint
      from: root.fromRect.y - root.pad; to: root.toRect.y - root.pad
    }
    NumberAnimation {
      target: glowBox; property: "width"; duration: root.moveMs; easing.type: Easing.OutQuint
      from: root.fromRect.width + root.pad * 2; to: root.toRect.width + root.pad * 2
    }
    NumberAnimation {
      target: glowBox; property: "height"; duration: root.moveMs; easing.type: Easing.OutQuint
      from: root.fromRect.height + root.pad * 2; to: root.toRect.height + root.pad * 2
    }
    SequentialAnimation {
      // From wherever light() got to: a lit glow holds, then goes.
      NumberAnimation { target: glowBox; property: "opacity"; to: 1; duration: Style.duration(240); easing.type: Easing.InQuad }
      NumberAnimation { target: glowBox; property: "opacity"; to: 0; duration: Style.duration(120); easing.type: Easing.OutQuad }
    }
  }
}
