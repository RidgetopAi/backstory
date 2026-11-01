import QtQuick
import QtQuick.Controls

// Minimal stand-in for qs.Ui.PanelToolTip — a plain Controls ToolTip with
// the real one's panel* overrides accepted and ignored.
ToolTip {
  property color panelForeground: "black"
  property color panelBackground: "white"
  property color panelBorder: "black"
  property string fontFamily: ""
  property real fontSize: 0

  delay: 400
}
