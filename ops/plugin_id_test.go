package ops

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The plugin id Backstory is listed under (the permanent marketplace id) and
// the one earlier releases installed under: the migration names it on purpose.
const (
	pluginID       = "ridgetopai.backstory"
	legacyPluginID = "backstory.this-week"
)

func (f *installFixture) legacyDir() string {
	return filepath.Join(f.cfg, "omarchy", "plugins", legacyPluginID)
}

func (f *installFixture) legacyBackup() string {
	return filepath.Join(f.cfg, "omarchy", "plugins", "."+legacyPluginID+".prev")
}

// seedLegacyPanel writes an old-id Backstory panel and its hidden backup.
func (f *installFixture) seedLegacyPanel() {
	f.t.Helper()
	for _, d := range []string{f.legacyDir(), f.legacyBackup()} {
		if err := os.MkdirAll(d, 0o755); err != nil { //nolint:gosec // fixture path under t.TempDir
			f.t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "manifest.json"), []byte(`{"id": "`+legacyPluginID+`"}`), 0o644); err != nil { //nolint:gosec // fixture path under t.TempDir
			f.t.Fatal(err)
		}
	}
}

// TestMakeInstallMigratesLegacyPanelID: an old backstory.this-week Backstory
// panel (and its .prev) is disabled and removed; only ridgetopai.backstory
// stays, enabled. A second install does nothing extra.
func TestMakeInstallMigratesLegacyPanelID(t *testing.T) {
	f := newInstallFixture(t)
	f.fakeOmarchy()
	f.seedLegacyPanel()

	f.mustMake("install")
	calls := f.calls()
	disable := "omarchy plugin disable " + legacyPluginID
	enable := "omarchy plugin enable " + pluginID
	di, ei := strings.Index(calls, disable), strings.Index(calls, enable)
	if di < 0 || ei < 0 || di > ei {
		t.Errorf("want the old id disabled, then the new one enabled:\n%s", calls)
	}
	mustNotExist(t, f.legacyDir())
	mustNotExist(t, f.legacyBackup())
	assertPanelRuntimeOnly(t, f.panelDir())
	entries, err := os.ReadDir(filepath.Dir(f.panelDir()))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), legacyPluginID) {
			t.Errorf("plugins dir still holds %s", e.Name())
		}
	}

	f.resetCalls()
	f.mustMake("install")
	if strings.Contains(f.calls(), "plugin disable") {
		t.Errorf("second install touched a plugin it should not have:\n%s", f.calls())
	}
	mustNotExist(t, f.legacyDir())
}

// TestMakeInstallLeavesForeignLegacyNamedFolder: a folder called
// backstory.this-week that is not a Backstory panel is not ours to touch.
func TestMakeInstallLeavesForeignLegacyNamedFolder(t *testing.T) {
	f := newInstallFixture(t)
	f.fakeOmarchy()
	if err := os.MkdirAll(f.legacyDir(), 0o755); err != nil { //nolint:gosec // fixture path under t.TempDir
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.legacyDir(), "manifest.json"), []byte(`{"id": "someone.else"}`), 0o644); err != nil { //nolint:gosec // fixture path under t.TempDir
		t.Fatal(err)
	}
	before := treeMap(t, f.legacyDir())

	f.mustMake("install")
	after := treeMap(t, f.legacyDir())
	if len(before) != len(after) || after["manifest.json"] != before["manifest.json"] {
		t.Errorf("foreign folder changed: %v -> %v", before, after)
	}
	if strings.Contains(f.calls(), "plugin disable "+legacyPluginID) {
		t.Errorf("disabled a plugin that is not Backstory's:\n%s", f.calls())
	}
	mustExist(t, f.panelDir())
}

// TestMakeUninstallRemovesLegacyPanelToo: uninstall removes the new id and a
// leftover old-id Backstory panel with its backup.
func TestMakeUninstallRemovesLegacyPanelToo(t *testing.T) {
	f := newInstallFixture(t)
	f.fakeOmarchy()
	f.mustMake("install")
	f.seedLegacyPanel()

	f.mustMake("uninstall")
	mustNotExist(t, f.panelDir())
	mustNotExist(t, f.legacyDir())
	mustNotExist(t, f.legacyBackup())
	if !strings.Contains(f.calls(), "omarchy plugin disable "+legacyPluginID) {
		t.Errorf("old id was not disabled:\n%s", f.calls())
	}
}

// TestPanelManifestIdentity: the manifest carries the marketplace id, the
// author, and the release version (the AUR pkgver), so a release bump that
// moves only one of the two fails here.
func TestPanelManifestIdentity(t *testing.T) {
	raw, err := os.ReadFile("../panel/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		ID, Author, Version string
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	pkg, err := os.ReadFile("aur/PKGBUILD")
	if err != nil {
		t.Fatal(err)
	}
	pm := regexp.MustCompile(`(?m)^pkgver=(\S+)$`).FindSubmatch(pkg)
	if pm == nil {
		t.Fatal("no pkgver in ops/aur/PKGBUILD")
	}
	if m.ID != pluginID {
		t.Errorf("manifest id = %q, want %q", m.ID, pluginID)
	}
	if m.Author != "RidgetopAi" {
		t.Errorf("manifest author = %q, want RidgetopAi", m.Author)
	}
	if m.Version != string(pm[1]) {
		t.Errorf("manifest version %q != PKGBUILD pkgver %q: a release bump moves both", m.Version, pm[1])
	}
}

// TestHyprlandTargetsNameThePanelID: every omarchy-shell target in the bind is
// the plugin id (or its .popup), and the QML declares that same id as the
// bar widget's moduleName and the panel's IPC target.
func TestHyprlandTargetsNameThePanelID(t *testing.T) {
	lua, err := os.ReadFile("hyprland/backstory.lua")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`omarchy-shell (?:shell (?:summon|toggle|hide) )?(\S+)`)
	ms := re.FindAllStringSubmatch(string(lua), -1)
	if len(ms) < 3 {
		t.Fatalf("found only %d omarchy-shell calls in the bind", len(ms))
	}
	for _, m := range ms {
		if id := strings.TrimSuffix(m[1], ".popup"); id != pluginID {
			t.Errorf("bind targets %q, want %s or %s.popup", m[1], pluginID, pluginID)
		}
	}
	for file, decl := range map[string]string{
		"../panel/Panel.qml":     `target: "` + pluginID + `"`,
		"../panel/BarWidget.qml": `moduleName: "` + pluginID + `"`,
	} {
		b, err := os.ReadFile(file) //nolint:gosec // this repo's own panel files
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), decl) {
			t.Errorf("%s does not declare %s", file, decl)
		}
	}
	b, _ := os.ReadFile("../panel/BarWidget.qml")
	if !strings.Contains(string(b), `target: "`+pluginID+`.popup"`) {
		t.Errorf("BarWidget.qml does not declare the %s.popup IPC target", pluginID)
	}
}
