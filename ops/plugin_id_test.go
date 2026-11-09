package ops

import (
	"encoding/json"
	"errors"
	"fmt"
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

// newestReleasedVersion returns the version of the first released
// "## [X.Y.Z] - YYYY-MM-DD" heading after "## [Unreleased]" in a changelog.
func newestReleasedVersion(changelog string) (string, error) {
	if !regexp.MustCompile(`(?m)^## \[Unreleased\]`).MatchString(changelog) {
		return "", errors.New("CHANGELOG.md has no ## [Unreleased] section")
	}
	_, after, _ := strings.Cut(changelog, "## [Unreleased]")
	m := regexp.MustCompile(`(?m)^## \[(\d+\.\d+\.\d+)\] - \d{4}-\d{2}-\d{2}\s*$`).FindStringSubmatch(after)
	if m == nil {
		return "", errors.New("CHANGELOG.md has no released ## [X.Y.Z] - YYYY-MM-DD section after [Unreleased]")
	}
	return m[1], nil
}

// panelManifestProblems checks panel/manifest.json and CHANGELOG.md under
// root (the repo root, or a temp copy of the files the check reads) and
// returns every problem found.
func panelManifestProblems(root string) []string {
	raw, err := os.ReadFile(filepath.Join(root, "panel", "manifest.json")) //nolint:gosec // this repo's own files
	if err != nil {
		return []string{err.Error()}
	}
	var m struct {
		ID, Author, Version string
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return []string{err.Error()}
	}
	var probs []string
	if m.ID != pluginID {
		probs = append(probs, fmt.Sprintf("manifest id = %q, want %q", m.ID, pluginID))
	}
	if m.Author != "RidgetopAi" {
		probs = append(probs, fmt.Sprintf("manifest author = %q, want RidgetopAi", m.Author))
	}
	cl, err := os.ReadFile(filepath.Join(root, "CHANGELOG.md")) //nolint:gosec // this repo's own files
	if err != nil {
		return append(probs, err.Error())
	}
	want, err := newestReleasedVersion(string(cl))
	if err != nil {
		return append(probs, err.Error())
	}
	if m.Version != want {
		probs = append(probs, fmt.Sprintf("manifest version %q != newest released CHANGELOG section %q: the release commit moves both", m.Version, want))
	}
	return probs
}

// TestPanelManifestIdentity: the manifest carries the marketplace id, the
// author, and the newest released CHANGELOG version (both move in the release
// commit, before the tag; the PKGBUILD pkgver moves after it).
func TestPanelManifestIdentity(t *testing.T) {
	for _, p := range panelManifestProblems("..") {
		t.Error(p)
	}
}

// TestPanelManifestVersionTracksChangelogNotPKGBUILD simulates a release
// commit in a temp copy of the files the check reads, with the PKGBUILD
// still at the previous pkgver.
func TestPanelManifestVersionTracksChangelogNotPKGBUILD(t *testing.T) {
	const rel = "## [9.9.9] - 2026-01-01\n\n### Changed\n\n- simulated\n\n"
	stage := func(t *testing.T, changelogReleased bool, manifestVersion string) string {
		t.Helper()
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "panel"), 0o755); err != nil { //nolint:gosec // fixture path under t.TempDir
			t.Fatal(err)
		}
		cl, err := os.ReadFile("../CHANGELOG.md")
		if err != nil {
			t.Fatal(err)
		}
		text := string(cl)
		if changelogReleased {
			text = strings.Replace(text, "## [Unreleased]", "## [Unreleased]\n\n"+rel, 1)
		}
		mf, err := os.ReadFile("../panel/manifest.json")
		if err != nil {
			t.Fatal(err)
		}
		mf = regexp.MustCompile(`"version"\s*:\s*"[^"]*"`).ReplaceAll(mf, []byte(`"version": "`+manifestVersion+`"`))
		for name, b := range map[string][]byte{"CHANGELOG.md": []byte(text), "panel/manifest.json": mf} {
			if err := os.WriteFile(filepath.Join(root, name), b, 0o644); err != nil { //nolint:gosec // fixture path under t.TempDir
				t.Fatal(err)
			}
		}
		pkg, err := os.ReadFile("aur/PKGBUILD")
		if err != nil {
			t.Fatal(err)
		}
		if !regexp.MustCompile(`(?m)^pkgver=1\.3\.0$`).Match(pkg) {
			t.Log("PKGBUILD pkgver is not 1.3.0; the check must ignore it regardless")
		}
		return root
	}
	const cur = "1.3.0"
	if p := panelManifestProblems(stage(t, true, "9.9.9")); len(p) != 0 {
		t.Errorf("release commit (CHANGELOG 9.9.9 + manifest 9.9.9) should be green: %v", p)
	}
	if p := panelManifestProblems(stage(t, true, cur)); len(p) == 0 {
		t.Error("CHANGELOG 9.9.9 with manifest still 1.3.0 should be red")
	}
	if p := panelManifestProblems(stage(t, false, "9.9.9")); len(p) == 0 {
		t.Error("manifest 9.9.9 with no CHANGELOG 9.9.9 section should be red")
	}
	if p := panelManifestProblems(stage(t, false, cur)); len(p) != 0 {
		t.Errorf("unmodified state should be green: %v", p)
	}
}

func TestNewestReleasedVersionFailsLoudlyWithoutReleasedSection(t *testing.T) {
	if _, err := newestReleasedVersion("# Changelog\n\n## [Unreleased]\n\n- x\n"); err == nil {
		t.Error("changelog with no released section must error")
	}
	if _, err := newestReleasedVersion("# Changelog\n\n## [1.0.0] - 2026-01-01\n"); err == nil {
		t.Error("changelog with no [Unreleased] section must error")
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
