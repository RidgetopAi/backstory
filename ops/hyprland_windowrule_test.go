package ops

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// panelBind is the panel toggle (Brian's call 2026-10-04): CTRL+SHIFT+B,
// because the old Super-based bind is Omarchy's stock Browser bind.
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

// TestHyprlandBindCommandDoesNotStartWithABracket: Hyprland's exec reads a
// leading [ ] as window rules (`exec [workspace 2] app`), so a bind command
// that opens with a shell test `[ ... ]` runs nothing (seen 2026-10-31 on
// Hyprland 0.56: the key did nothing while the same command after a prefix
// worked). The bind's shell test is written `test ...` instead.
func TestHyprlandBindCommandDoesNotStartWithABracket(t *testing.T) {
	raw, err := os.ReadFile("hyprland/backstory.lua")
	if err != nil {
		t.Fatalf("read backstory.lua: %v", err)
	}
	m := regexp.MustCompile(`hl\.dsp\.exec_cmd\("\s*(.)`).FindAllStringSubmatch(string(raw), -1)
	if len(m) == 0 {
		t.Fatal("no hl.dsp.exec_cmd(\"...\") in hyprland/backstory.lua")
	}
	for _, c := range m {
		if c[1] == "[" {
			t.Errorf("an exec_cmd command starts with [, which Hyprland reads as window rules: %q", c[0])
		}
	}
}

// No tracked file but the CHANGELOG (history) may still pair the old Omarchy
// Browser bind with Backstory. The pattern is assembled so this file does not
// match itself.
func TestNoTrackedFileStillNamesOldBind(t *testing.T) {
	old := regexp.MustCompile(`(?i)super\s*\+\s*shift\s*\+\s*b\b`)
	out, err := gitLsFilesAtRoot()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	scanned := 0
	for _, rel := range strings.Split(strings.TrimRight(string(out), "\x00"), "\x00") {
		if rel == "CHANGELOG.md" || rel == "" {
			continue
		}
		b, err := os.ReadFile("../" + rel) //nolint:gosec // tracked path in this repo
		if err != nil {
			continue // deleted in the working tree, or a directory entry
		}
		scanned++
		if loc := old.FindIndex(b); loc != nil {
			t.Errorf("%s still names the old bind %q", rel, b[loc[0]:loc[1]])
		}
	}
	if scanned < 10 {
		t.Fatalf("scanned only %d tracked files; the guard is not reading the repo", scanned)
	}
}

// gitLsFilesAtRoot lists tracked files relative to the repo root (the test
// runs in ops/, so --full-name plus a "../" prefix addresses them correctly).
func gitLsFilesAtRoot() ([]byte, error) {
	cmd := exec.Command("git", "ls-files", "-z", "--full-name", "--", "..")
	return cmd.Output()
}

// TestHyprlandWindowruleMatchesPanelClass pins tasks 93f7c6fd/2606210b: the
// shipped Lua windowrule floats and sizes the window, matching class
// org.quickshell AND the exact title Panel.qml's windowTitle sets. The class
// and title are read from the rule's own selector, comments stripped, so a
// title left only in a comment or another rule's class does not pass.
func TestHyprlandWindowruleMatchesPanelClass(t *testing.T) {
	if _, err := os.Stat("hyprland/backstory.conf"); err == nil {
		t.Error("backstory.conf must not exist (Omarchy uses Lua config)")
	}
	rule := panelWindowRule(t)
	if class := luaStringField(rule.selector, "class"); class != `^(org\\.quickshell)$` {
		t.Errorf("the window's rule matches class %s, want ^(org\\\\.quickshell)$", class)
	}
	if !regexp.MustCompile(`float\s*=\s*true`).MatchString(rule.effects) {
		t.Error("the window's rule must set float = true")
	}
	if !regexp.MustCompile(`size\s*=\s*\{\s*\d+\s*,\s*\d+\s*\}`).MatchString(rule.effects) {
		t.Error("the window's rule must set a size")
	}
	// Task ddbbe8b7: helpers is not a resolvable module on Omarchy (it defines
	// the global `o`), so a require( call makes hyprland.lua error. Comments
	// are stripped so the install note may mention require.
	if regexp.MustCompile(`\brequire\s*[("']`).MatchString(luaCode(t)) {
		t.Error("lua must not call require( — use the global o")
	}
}

// TestHyprlandWindowruleHidesThePopOutUntilPlaced: a pop-out opens the window
// under Panel.qml's windowTitle plus "-popout", which the rule floats and
// draws with no decorations, blur or animation, so nothing shows until the
// window sits where the popup's card is (seen 2026-10-31: without it, the
// border and glow showed for two frames at the window's usual place).
func TestHyprlandWindowruleHidesThePopOutUntilPlaced(t *testing.T) {
	want := "^(" + panelWindowTitle(t) + "-popout)$"
	var effects []string
	for _, r := range windowRules(t, luaCode(t)) {
		if title := luaStringField(r.selector, "title"); title == want {
			effects = append(effects, r.effects)
		}
	}
	if len(effects) != 1 {
		t.Fatalf("hyprland/backstory.lua has %d o.window rules matching the pop-out title %q, want 1", len(effects), want)
	}
	for _, re := range []string{`float\s*=\s*true`, `no_anim\s*=\s*true`, `decorate\s*=\s*false`, `no_blur\s*=\s*true`} {
		if !regexp.MustCompile(re).MatchString(effects[0]) {
			t.Errorf("the pop-out rule lacks %s:\n%s", re, effects[0])
		}
	}
}

// TestHyprlandWindowruleOpensWithoutAnimation: Hyprland does not animate the
// panel window, as it does not animate Omarchy's own panels
// (default/hypr/apps/omarchy-shell.lua sets no_anim for them). Those panels
// fade their card in themselves in QML, and so does Panel.qml
// (tst_panel_window.qml test_card_fades_in_on_every_open). Hyprland's window
// animation took about 300 ms to settle.
func TestHyprlandWindowruleOpensWithoutAnimation(t *testing.T) {
	if !regexp.MustCompile(`no_anim\s*=\s*true`).MatchString(mainWindowRule(t)) {
		t.Error("the window's rule must set no_anim = true")
	}
}

// mainWindowRule is the effects of the window's own rule (see
// panelWindowRule): the pop-out rule sets some of the same effects, so a
// check of the whole file would pass with them gone from this one.
func mainWindowRule(t *testing.T) string {
	t.Helper()
	return panelWindowRule(t).effects
}

// panelWindowRule is the one o.window rule whose selector matches exactly
// the title Panel.qml's windowTitle sets.
func panelWindowRule(t *testing.T) windowRule {
	t.Helper()
	want := "^(" + panelWindowTitle(t) + ")$"
	var found []windowRule
	for _, r := range windowRules(t, luaCode(t)) {
		if title := luaStringField(r.selector, "title"); title == want {
			found = append(found, r)
		}
	}
	if len(found) != 1 {
		t.Fatalf("hyprland/backstory.lua has %d o.window rules matching title %q, want 1", len(found), want)
	}
	return found[0]
}

// panelWindowTitle is the title Panel.qml's windowTitle sets.
func panelWindowTitle(t *testing.T) string {
	t.Helper()
	qml, err := os.ReadFile("../panel/Panel.qml")
	if err != nil {
		t.Fatalf("read Panel.qml: %v", err)
	}
	m := regexp.MustCompile(`readonly property string windowTitle: "([^"]+)"`).FindSubmatch(qml)
	if m == nil {
		t.Fatal("Panel.qml declares no windowTitle")
	}
	return string(m[1])
}

// luaCode is hyprland/backstory.lua with its comments stripped.
func luaCode(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("hyprland/backstory.lua")
	if err != nil {
		t.Fatalf("read backstory.lua: %v", err)
	}
	return regexp.MustCompile(`(?m)--.*$`).ReplaceAllString(string(raw), "")
}

// windowRule is one o.window(selector, effects) call: the inside of the
// selector table (or of the local table the call names) and of the effects
// table.
type windowRule struct{ selector, effects string }

// windowRules reads every o.window(...) call in code. Tables are matched by
// their braces, so nested tables and one-line rules read as Lua reads them.
func windowRules(t *testing.T, code string) []windowRule {
	t.Helper()
	var rules []windowRule
	ident := regexp.MustCompile(`^[A-Za-z_]\w*`)
	comma := regexp.MustCompile(`^\s*,\s*`)
	for _, loc := range regexp.MustCompile(`\bo\.window\(\s*`).FindAllStringIndex(code, -1) {
		rest := code[loc[1]:]
		var sel string
		if id := ident.FindString(rest); id != "" {
			def := regexp.MustCompile(`\blocal\s+` + id + `\s*=\s*\{`).FindStringIndex(code)
			if def == nil {
				t.Fatalf("o.window(%s, ...) names no local table", id)
			}
			sel, _ = luaTable(code[def[1]-1:])
			rest = rest[len(id):]
		} else {
			var n int
			sel, n = luaTable(rest)
			rest = rest[n:]
		}
		c := comma.FindString(rest)
		if c == "" {
			t.Fatalf("an o.window call has no effects table: %.60q", code[loc[0]:])
		}
		eff, n := luaTable(rest[len(c):])
		if n == 0 {
			t.Fatalf("an o.window call has no effects table: %.60q", code[loc[0]:])
		}
		rules = append(rules, windowRule{sel, eff})
	}
	return rules
}

// luaTable is the inside of the table s opens with, and the length of s the
// table spans; 0 when s does not open with a whole table. Braces inside
// strings are not told apart (backstory.lua's rules have none).
func luaTable(s string) (string, int) {
	if !strings.HasPrefix(s, "{") {
		return "", 0
	}
	depth := 0
	for i, c := range s {
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[1:i], i + 1
			}
		}
	}
	return "", 0
}

// luaStringField is the string value of key in a table's inside, or "" when it has none.
func luaStringField(table, key string) string {
	m := regexp.MustCompile(`(?:^|[\s,{])` + regexp.QuoteMeta(key) + `\s*=\s*"([^"]*)"`).FindStringSubmatch(table)
	if m == nil {
		return ""
	}
	return m[1]
}

// TestHyprlandWindowruleKeepsTheThemesShadow: the window is styled as any
// other window, so it keeps the theme's shadow (Norrsken's focus "glow").
// It once dropped it because, with no_anim, closing left the glow on screen
// as a ghost outline; on Hyprland 0.56 closing leaves none (seen 2026-10-31).
func TestHyprlandWindowruleKeepsTheThemesShadow(t *testing.T) {
	raw, err := os.ReadFile("hyprland/backstory.lua")
	if err != nil {
		t.Fatalf("read backstory.lua: %v", err)
	}
	code := regexp.MustCompile(`(?m)--.*$`).ReplaceAllString(string(raw), "")
	if regexp.MustCompile(`no_shadow\s*=`).MatchString(code) {
		t.Error("lua window rule must not set no_shadow: the window takes the theme's shadow")
	}
}

// TestHyprlandBindCallsIPCThePanelAnswers: every `omarchy-shell <target>
// <method>` in the bind (other than the shell's own `shell`) names an IPC
// target a panel QML file declares, with that function, so renaming either
// side fails here instead of leaving the key doing nothing (found by review).
func TestHyprlandBindCallsIPCThePanelAnswers(t *testing.T) {
	raw, err := os.ReadFile("hyprland/backstory.lua")
	if err != nil {
		t.Fatalf("read backstory.lua: %v", err)
	}
	calls := regexp.MustCompile(`omarchy-shell (\S+) (\w+)`).FindAllStringSubmatch(string(raw), -1)
	files, err := filepath.Glob("../panel/*.qml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no panel QML files: %v", err)
	}
	checked := 0
	for _, c := range calls {
		target, method := c[1], c[2]
		if target == "shell" {
			continue
		}
		checked++
		handler := regexp.MustCompile(`(?s)target:\s*"` + regexp.QuoteMeta(target) + `"(.*?)\n  \}`)
		found := false
		for _, f := range files {
			b, err := os.ReadFile(f) //nolint:gosec // this repo's own panel files
			if err != nil {
				t.Fatal(err)
			}
			if m := handler.FindSubmatch(b); m != nil && regexp.MustCompile(`function\s+`+method+`\s*\(`).Match(m[1]) {
				found = true
			}
		}
		if !found {
			t.Errorf("the bind calls %s %s, but no panel QML file declares that IPC target and function", target, method)
		}
	}
	if checked < 2 {
		t.Fatalf("checked only %d IPC calls in the bind; the pattern is not reading it", checked)
	}
}

// TestHyprlandBindTestsForTheWordThePanelAnswers: the bind's first step goes on to
// the popup unless `omarchy-shell <target> <method>` prints the word it
// tests for, so that word is what Panel.qml's method returns once it has
// acted on an open window (its last return). Changing either side alone
// would leave the key opening the popup over an open window.
func TestHyprlandBindTestsForTheWordThePanelAnswers(t *testing.T) {
	m := regexp.MustCompile(`"\$\(omarchy-shell \S+ (\w+)[^)]*\)\\?"\s*=\s*(\w+)`).FindStringSubmatch(luaCode(t))
	if m == nil {
		t.Fatal("the bind does not test an omarchy-shell call's output against a word")
	}
	method, word := m[1], m[2]
	qml, err := os.ReadFile("../panel/Panel.qml")
	if err != nil {
		t.Fatalf("read Panel.qml: %v", err)
	}
	def := regexp.MustCompile(`function\s+` + method + `\s*\(\)\s*\{`).FindIndex(qml)
	if def == nil {
		t.Fatalf("Panel.qml defines no function %s() {", method)
	}
	body, n := luaTable(string(qml[def[1]-1:]))
	if n == 0 {
		t.Fatalf("Panel.qml's %s has no closed body", method)
	}
	returns := regexp.MustCompile(`return\s+"(\w+)"`).FindAllStringSubmatch(body, -1)
	if len(returns) == 0 {
		t.Fatalf("Panel.qml's %s returns no word:\n%s", method, body)
	}
	if last := returns[len(returns)-1][1]; last != word {
		t.Errorf("the bind tests %s for %q, but Panel.qml's %s answers %q once it acts", method, word, method, last)
	}
}

// TestHyprlandBindSummonsThePanelPlugin: the bind's last step summons the
// plugin by id, which must be the id panel/manifest.json declares, or the
// key does nothing with no Backstory widget in the bar.
func TestHyprlandBindSummonsThePanelPlugin(t *testing.T) {
	m := regexp.MustCompile(`omarchy-shell shell summon (\S+)`).FindStringSubmatch(luaCode(t))
	if m == nil {
		t.Fatal("the bind never runs omarchy-shell shell summon")
	}
	raw, err := os.ReadFile("../panel/manifest.json")
	if err != nil {
		t.Fatalf("read manifest.json: %v", err)
	}
	var manifest struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("parse manifest.json: %v", err)
	}
	if manifest.ID == "" || m[1] != manifest.ID {
		t.Errorf("the bind summons %q, but panel/manifest.json's id is %q", m[1], manifest.ID)
	}
}
