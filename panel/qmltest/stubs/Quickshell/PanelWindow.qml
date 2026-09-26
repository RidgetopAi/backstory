import QtQuick

// Minimal stand-in for Quickshell's PanelWindow — a layer-shell surface,
// not a QtQuick.Item (real Quickshell backs it with QQuickWindow, not
// QQuickItem — this is why it owns its OWN "anchors"/"margins" grouped
// properties below instead of QtQuick.Item's own incompatible ones: Item's
// built-in `anchors` binds to AnchorLine values, "top: true" is a bool,
// assigning one to the other is a QML compile error, proven empirically
// while building this harness). "as an Item with implicit size" (task
// 4fe02e30's DONE WHEN clause 1): contentItem is the real Item every
// default-property child gets reparented onto below, so ordinary QtQuick
// geometry (x, y, mapToItem) works for the layout-bounds walker (DONE WHEN
// clause 3) exactly as it would for real child items inside a real window.
//
// Deliberately does NOT expose a `WlrLayershell` property — Panel.qml
// reads `panel.WlrLayershell` defensively (see Panel.qml's own comment,
// following real Quickshell/WlrLayershell's documented
// "if (this.WlrLayershell != null)" portability pattern for compositors
// that don't back PanelWindow with wlr-layer-shell). A plain property read
// for a name this object never declares evaluates to `undefined` in
// QML/JS — no warning, no error — which is exactly the "not backed by
// wlr-layer-shell" case this stub represents. Reproducing a real,
// C++-registered WlrLayershell ATTACHED property is not possible from pure
// QML (Qt's QML_ATTACHED mechanism has no QML-only equivalent — also
// proven empirically while building this harness, including via a
// qmldir-declared qmltypes attachedType hint, which qmltestrunner's engine
// still rejects at runtime with "Non-existent attached object").
// Grouped-property syntax (`anchors { top: true; right: true }`) needs its
// target property's STATIC type to expose those sub-properties at compile
// time — a bare `property QtObject anchors: QtObject { property bool
// top... }` only works for the FIRST sub-property assigned per block in
// practice (proven empirically while building this harness: `anchors {
// top: true; right: true }` loaded fine, but the very next block, `margins
// { top: ...; right: ... }`, failed on its second field with "Cannot
// assign to non-existent property 'right'"). PanelWindowAnchors.qml /
// PanelWindowMargins.qml give the compiler real static types to check
// against (this Qt build's `component Name: Base {...}` inline-component
// syntax was tried first and rejected at parse time by both qmllint and
// qmltestrunner — real separate files, not a Qt-version workaround).
QtObject {
  id: window

  property PanelWindowAnchors anchors: PanelWindowAnchors {}
  property PanelWindowMargins margins: PanelWindowMargins {}

  property bool visible: false
  property color color: "transparent"
  property real implicitWidth: 0
  property real implicitHeight: 0
  property int exclusionMode: 0

  default property list<QtObject> data

  readonly property Item contentItem: _content

  property Item _content: Item {
    x: 0
    y: 0
    width: window.implicitWidth
    height: window.implicitHeight
  }

  Component.onCompleted: {
    for (var i = 0; i < window.data.length; i++) {
      var child = window.data[i]
      if (child && ("parent" in child)) {
        child.parent = window._content
      }
    }
  }
}
