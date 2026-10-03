package ops

import (
	"os"
	"regexp"
	"testing"
)

// TestHyprlandWindowruleMatchesPanelClass pins tasks 93f7c6fd/2606210b: the
// shipped Lua windowrule floats and sizes the window, matching class
// org.quickshell AND the exact title Panel.qml's windowTitle sets.
func TestHyprlandWindowruleMatchesPanelClass(t *testing.T) {
	qml, err := os.ReadFile("../panel/Panel.qml")
	if err != nil {
		t.Fatalf("read Panel.qml: %v", err)
	}
	m := regexp.MustCompile(`readonly property string windowTitle: "([^"]+)"`).FindSubmatch(qml)
	if m == nil {
		t.Fatal("Panel.qml declares no windowTitle")
	}
	title := string(m[1])

	if _, err := os.Stat("hyprland/backstory.conf"); err == nil {
		t.Error("backstory.conf must not exist (Omarchy uses Lua config)")
	}
	raw, err := os.ReadFile("hyprland/backstory.lua")
	if err != nil {
		t.Fatalf("read backstory.lua: %v", err)
	}
	lua := string(raw)

	if !regexp.MustCompile(`title\s*=\s*"\^\(` + regexp.QuoteMeta(title) + `\)\$"`).MatchString(lua) {
		t.Errorf("lua title pattern does not match Panel.qml windowTitle %q", title)
	}
	if !regexp.MustCompile(`class\s*=\s*"\^\(org\\\\\.quickshell\)\$"`).MatchString(lua) {
		t.Error("lua must match class ^(org\\\\.quickshell)$")
	}
	if !regexp.MustCompile(`float\s*=\s*true`).MatchString(lua) {
		t.Error("lua must set float = true")
	}
	if !regexp.MustCompile(`size\s*=\s*\{\s*\d+\s*,\s*\d+\s*\}`).MatchString(lua) {
		t.Error("lua must set a size")
	}
	if !regexp.MustCompile(`o\.window\(|hl\.window_rule\(`).MatchString(lua) {
		t.Error("lua must call o.window or hl.window_rule")
	}
}
