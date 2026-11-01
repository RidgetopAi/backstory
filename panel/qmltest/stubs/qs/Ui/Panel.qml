import QtQuick

// Minimal stand-in for qs.Ui.Panel: the open/close lifecycle and panel
// switching a bar-widget popout's root gets from the real base.
Item {
  id: root

  property QtObject bar: null
  property string moduleName: ""
  property var settings: ({})
  property string ipcTarget: ""
  property bool manageIpc: true
  property bool popoutSwitchClosing: false
  property bool _open: false

  readonly property bool opened: _open

  function open() { _open = true }
  function close() { _open = false }
  function closeForPopoutSwitch() { close() }
  function toggle() { opened ? close() : open() }
  function switchPanel(direction) {
    if (bar && typeof bar.switchPanelFrom === "function") return bar.switchPanelFrom(root, direction)
    return false
  }
}
