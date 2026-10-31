import QtQuick
import qs.Commons
import qs.Commons as Commons

// A small "from <device>" chip for a record or handoff that came from another
// device. `device` is the origin_device_name read through js/model.js or
// js/records.js; "" (a local record, JSON null) hides the chip entirely.
Rectangle {
  id: root

  property string device: ""

  visible: root.device !== ""
  implicitWidth: label.implicitWidth + Style.space(6)
  implicitHeight: label.implicitHeight + Style.space(2)
  radius: height / 2
  color: "transparent"
  border.width: 1
  border.color: Qt.darker(Commons.Color.foreground, 1.6)

  Text {
    id: label
    objectName: "deviceChipText"
    anchors.centerIn: parent
    textFormat: Text.PlainText
    text: "from " + root.device
    font.family: Style.font.family
    font.pixelSize: Style.font.caption
    color: Qt.darker(Commons.Color.foreground, 1.3)
  }
}
