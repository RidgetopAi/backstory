import QtQuick
import QtQuick.Controls.Basic as QQC2

// Minimal stand-in for qs.Ui.TextField — GroupEditor.qml's group-name entry
// field. QtQuick.Controls' own TextField already has placeholderText,
// placeholderTextColor, selectionColor, background, text and onTextChanged
// (see qs/Commons/Style.qml's own doc comment for why this stays minimal
// rather than a full reimplementation); the Basic style avoids pulling in a
// native platform style this offscreen test has no window to render into.
QQC2.TextField {
}
