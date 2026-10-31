package panel

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// bareColor matches a theme colour read through a bare `Color.`: one not
// preceded by an identifier character or a dot, so `Commons.Color.popups`
// and `someColor.r` do not match. Qt 6.12's QtQuick exports its own Color
// singleton, which under qmltestrunner shadows the qs.Commons.Color
// stand-in for a bare name, so every such read is undefined in the QML
// suite there. CI's older Qt has no such singleton and cannot see it.
var bareColor = regexp.MustCompile(`(^|[^.\w])Color\.`)

// TestPanelQMLReadsCommonsColor fails if any panel/*.qml reads the theme
// through a bare Color. instead of Commons.Color. (with `import qs.Commons
// as Commons`, as the host's own Ui files do).
func TestPanelQMLReadsCommonsColor(t *testing.T) {
	files, err := filepath.Glob("*.qml")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no panel QML files found")
	}
	for _, path := range files {
		raw, err := os.ReadFile(path) //nolint:gosec // path is this test's own filepath.Glob("*.qml") result, never external input
		if err != nil {
			t.Fatal(err)
		}
		for _, hit := range findQMLCode(path, string(raw), bareColor) {
			t.Errorf("bare Color. (read Commons.Color. instead): %s", hit)
		}
	}
}
