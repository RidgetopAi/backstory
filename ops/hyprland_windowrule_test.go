package ops

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// panelBind is the panel toggle (Brian's call 2026-10-04): CTRL+SHIFT+B,
// because SUPER+SHIFT+B is Omarchy's stock Browser bind.
const panelBind = "CTRL + SHIFT + B"

func TestHyprlandBindsPanelToggleToCtrlShiftB(t *testing.T) {
	raw, err := os.ReadFile("hyprland/backstory.lua")
	if err != nil {
		t.Fatal(err)
	}
	want := `local BACKSTORY_BIND = "` + panelBind + `"`
	if !strings.Contains(string(raw), want) {
		t.Errorf("hyprland/backstory.lua lacks %s", want)
	}
}

// No tracked file but the CHANGELOG (history) may still pair the old Omarchy
// Browser bind with Backstory. The pattern is assembled so this file does not
// match itself.
func TestNoTrackedFileStillNamesOldBind(t *testing.T) {
	old := regexp.MustCompile(`(?i)super\s*\+\s*shift\s*\+\s*b\b`)
	out, err := exec.Command("git", "ls-files", "-z").Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	for _, rel := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if rel == "CHANGELOG.md" || rel == "" {
			continue
		}
		b, err := os.ReadFile("../" + rel) //nolint:gosec // tracked path in this repo
		if err != nil {
			continue // deleted in the working tree, or a directory entry
		}
		if loc := old.FindIndex(b); loc != nil {
			t.Errorf("%s still names the old bind %q", rel, b[loc[0]:loc[1]])
		}
	}
}

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
	// Task ddbbe8b7: helpers is not a resolvable module on Omarchy (it defines
	// the global `o`), so a require( call makes hyprland.lua error. Comments
	// are stripped so the install note may mention require.
	code := regexp.MustCompile(`(?m)--.*$`).ReplaceAllString(lua, "")
	if regexp.MustCompile(`\brequire\s*[("']`).MatchString(code) {
		t.Error("lua must not call require( — use the global o")
	}
}
