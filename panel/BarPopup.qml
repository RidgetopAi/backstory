import QtQuick
import qs.Ui
import qs.Commons

// The bar widget's popup: a KeyboardPanel card holding ThisWeekView, as
// Omarchy's own bar panels are. KeyboardPanel's `content`, `loadedContent`
// and `loadedContentHeight` are Omarchy 4.5+ (4.0's has none of them), so
// this is its own file: BarWidget.qml probes it with Qt.createComponent and,
// where it will not load, a bar click opens the window instead. `host` is
// the BarWidget (a qs.Ui Panel) whose opened state, close() and switchPanel()
// the popup follows; `anchorItem` is the bar button the card hangs from.
Item {
  id: root

  property var host: null
  property Item anchorItem: null

  // BarWidget's handle onto the card (its screen, size and origin).
  readonly property var panel: popup

  KeyboardPanel {
    id: popup
    anchorItem: root.anchorItem
    owner: root.host
    bar: root.host ? root.host.bar : null
    open: root.host ? root.host.opened : false
    focusTarget: popup.loadedContent
    contentWidth: popup.fittedContentWidth(Style.space(380))
    contentHeight: popup.fittedContentHeight(popup.loadedContentHeight, Style.space(640))

    content: Item {
      implicitHeight: view.implicitHeight
      focus: true

      // Tab and Shift+Tab move to the next bar panel; the view takes the rest.
      function handleKey(key, modifiers) {
        if (key === Qt.Key_Tab || key === Qt.Key_Backtab) {
          root.host.switchPanel(key === Qt.Key_Backtab || (modifiers & Qt.ShiftModifier) ? -1 : 1)
          return true
        }
        return view.handleKey(key, modifiers)
      }

      Keys.onPressed: (event) => { event.accepted = handleKey(event.key, event.modifiers) }

      ThisWeekView {
        id: view
        anchors.fill: parent
        visible: !(root.host && root.host.handedOver)
        edge: 0
        footerBottomMargin: 0
        active: root.host ? root.host.opened : false
        canPopOut: true
        onCloseRequested: root.host.close()
        onPopOutRequested: root.host.popOut(view.snapshot())
      }
    }
  }
}
