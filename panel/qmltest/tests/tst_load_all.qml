import QtQuick
import QtTest

// Loads every panel/*.qml through the stub modules (task 4fe02e30 DONE
// WHEN clause 2). A file that fails to compile (a reference to a symbol a
// stub lacks — see qs/Ui/qmldir and friends' own doc comments) leaves
// Loader.item null; combined with QT_FATAL_WARNINGS=1 (make check's own
// invocation, panel/qmltest/Makefile) any RUNTIME warning anywhere in this
// whole suite — a binding TypeError, a binding loop — aborts the process
// with a non-zero exit before this test would even get to report which
// assertion caught it, which is the strongest "fails on ANY QML warning"
// guarantee this harness can give without a QQmlEngine.warnings hook (no
// such hook is reachable from pure QML — every avenue tried while
// building this harness needed C++, unavailable in this environment; see
// qmltest/stubs/Quickshell/PanelWindow.qml's own doc comment for the same
// wall hit on WlrLayershell).
TestCase {
  id: testCase
  name: "LoadAllPanelQml"

  readonly property var panelFiles: [
    "AttentionSection.qml",
    "BarWidget.qml",
    "GroupEditor.qml",
    "GroupProjectRow.qml",
    "MemoryButton.qml",
    "MemoryView.qml",
    "Panel.qml",
    "ProjectRow.qml",
    "WeekSection.qml",
    "WhereLeftOffSection.qml"
  ]

  Component {
    id: loaderComponent
    Loader {}
  }

  function test_loads_without_error_data() {
    var rows = []
    for (var i = 0; i < panelFiles.length; i++) rows.push({ tag: panelFiles[i], file: panelFiles[i] })
    return rows
  }

  function test_loads_without_error(data) {
    var loader = loaderComponent.createObject(null, { source: Qt.resolvedUrl("../../" + data.file) })
    tryCompare(loader, "status", Loader.Ready, 2000)
    verify(loader.item !== null, data.file + " failed to load: status=" + loader.status)
    loader.destroy()
  }
}
