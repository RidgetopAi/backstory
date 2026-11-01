import QtQuick

// Minimal stand-in for Qt's MultiEffect (QtQuick.Effects, Qt 6.5+): CI's
// Ubuntu Qt 6.4 has no QtQuick.Effects, so PopOutGlow.qml would not load.
// It draws nothing; the tests check the glow's motion, not its blur.
Item {
  property Item source: null
  property bool autoPaddingEnabled: true
  property bool blurEnabled: false
  property real blur: 0
  property int blurMax: 32
  property real brightness: 0
}
