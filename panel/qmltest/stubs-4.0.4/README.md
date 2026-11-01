# Omarchy 4.0.4 stub overlay

`make panel-qml-test` runs the suite twice. The second run uses the `stubs/`
tree (Omarchy 4.5) with this directory laid over it and these removed or cut:

- `qs/Ui/OverlayWindow.qml` is deleted (and its qmldir line cut): 4.0 has none.
- `qs/Commons/Style.qml` loses `duration` (cut by the Makefile's sed).
- `qs/Ui/KeyboardPanel.qml` and `qs/Ui/BarIconButton.qml` here replace the 4.5
  ones: no `content`/`loadedContent`/`barThickness`/`drawnHeight`, no `labelText`.

Only `tests-4.0.4/` runs against it; the 4.5 suite needs the 4.5 surface.
