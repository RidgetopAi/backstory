package ops

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestHyprlandWindowruleMatchesPanelClass pins task 93f7c6fd's clause 2: the
// shipped windowrule floats, sizes and places the window by the exact class
// Panel.qml's windowClass sets.
func TestHyprlandWindowruleMatchesPanelClass(t *testing.T) {
	qml, err := os.ReadFile("../panel/Panel.qml")
	if err != nil {
		t.Fatalf("read Panel.qml: %v", err)
	}
	m := regexp.MustCompile(`readonly property string windowClass: "([^"]+)"`).FindSubmatch(qml)
	if m == nil {
		t.Fatal("Panel.qml declares no windowClass")
	}
	class := string(m[1])

	conf, err := os.ReadFile("hyprland/backstory.conf")
	if err != nil {
		t.Fatalf("read backstory.conf: %v", err)
	}
	var float, size bool
	for _, line := range strings.Split(string(conf), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || !strings.HasPrefix(line, "windowrule") {
			continue
		}
		if !strings.Contains(line, "match:class ^("+regexp.QuoteMeta(class)+")$") {
			t.Errorf("rule does not match class %q: %s", class, line)
		}
		float = float || strings.Contains(line, "float on")
		size = size || strings.Contains(line, "size ")
	}
	if !float || !size {
		t.Errorf("rules must float and size the window (float=%v size=%v)", float, size)
	}
}
