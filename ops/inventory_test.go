package ops

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/homesnap"
	"github.com/RidgetopAi/backstory/internal/install"
)

// makeInstallInventory is every path under $HOME that `make install` /
// `make uninstall` (ops/install.sh) writes, replaces or deletes — including
// what the programs it runs write there — and what an edited, symlinked or
// shipped copy gets. TestMakeInstallInventory observes real runs in the
// fixture and fails on a path missing here or a row no run touched.
// Its population is what those runs do: a write on a branch they do not take
// is not seen, so a new branch that writes under $HOME needs a step here.
var makeInstallInventory = []homesnap.Row{
	{Pattern: ".local", Writer: "make install", OnInstall: "created when missing", OnRemove: "kept"},
	{Pattern: ".local/bin", Writer: "make install", OnInstall: "created when missing", OnRemove: "kept"},
	{Pattern: ".local/bin/backstory", Writer: "make install", OnInstall: "Backstory's own program at BINDIR: replaced by the built binary every install (via backstory.new, renamed into place)", OnRemove: "deleted"},
	{Pattern: ".local/bin/backstory.prev", Writer: "make install", OnInstall: "Backstory's rollback copy of the previous binary, replaced each upgrade", OnRemove: "deleted"},
	{Pattern: ".config/systemd", Writer: "make install", OnInstall: "created when missing", OnRemove: "kept"},
	{Pattern: ".config/systemd/user", Writer: "make install", OnInstall: "created when missing", OnRemove: "kept"},
	{Pattern: ".config/systemd/user/backstory.service", Writer: "make install", OnInstall: "shipped-or-keep (UNIT_SHIPPED_SHA256): shipped -> replaced; edited or symlinked -> kept, the new unit written beside it as .new", OnRemove: "shipped -> deleted; edited or symlinked -> kept and named"},
	{Pattern: ".config/systemd/user/backstory.service.new", Writer: "make install", OnInstall: "written only beside a kept unit; removed once the unit is replaced", OnRemove: "deleted"},
	{Pattern: ".config/systemd/user/backstory-sync.*", Writer: "backstory install sync", OnInstall: "not written by make install", OnRemove: "shipped-or-keep (SYNC_*_SHIPPED_SHA256, ExecStart binary undone): shipped -> deleted; edited or symlinked -> kept and named"},
	{Pattern: ".config/hypr", Writer: "make install", OnInstall: "created when missing", OnRemove: "kept"},
	{Pattern: ".config/hypr/backstory.lua", Writer: "make install", OnInstall: "shipped-or-keep (HYPR_SHIPPED_SHA256), as backstory.service", OnRemove: "shipped -> deleted; edited or symlinked -> kept and named"},
	{Pattern: ".config/hypr/backstory.lua.new", Writer: "make install", OnInstall: "as backstory.service.new", OnRemove: "deleted"},
	{Pattern: ".config/omarchy/plugins/ridgetopai.backstory/**", Writer: "make install", OnInstall: "Backstory's panel, replaced as a unit: the previous copy moves to .ridgetopai.backstory.prev; a git checkout (omarchy plugin add) is left alone; a symlink or a folder without our manifest id stops the install", OnRemove: "deleted, unless a git checkout, a symlink or not ours"},
	{Pattern: ".config/omarchy/plugins/.ridgetopai.backstory.prev/**", Writer: "make install", OnInstall: "the one-generation backup of the panel; replaced on the next upgrade unless it is a git checkout", OnRemove: "deleted unless a git checkout"},
	{Pattern: ".config/omarchy/plugins/backstory.this-week/**", Writer: "make install", OnInstall: "the pre-rename panel: deleted only when it is a real folder holding our legacy manifest id", OnRemove: "same"},
	{Pattern: ".config/omarchy/plugins/.backstory.this-week.prev/**", Writer: "make install", OnInstall: "its backup: deleted unless a git checkout", OnRemove: "same"},
	{Pattern: ".local/share/**", Writer: "the backstory daemon make install starts", OnInstall: "the memory store and its automatic backups; never written by the script", OnRemove: "never deleted (uninstall says so)"},
	{Pattern: ".local/state/**", Writer: "the backstory binary", OnInstall: "harness usage state (internal/usage); never written by the script", OnRemove: "kept"},
	{Pattern: ".config/go/**", Writer: "the go toolchain make install runs", OnInstall: "Go's own telemetry state, not Backstory's", OnRemove: "kept"},
}

var inventoryProbe = os.Getenv("INVENTORY_PRINT") != ""

func TestMakeInstallInventory(t *testing.T) {
	rows := makeInstallInventory
	f := newInstallFixture(t)
	f.seedLegacyPanel()
	var seen []string
	step := func(name string, run func()) {
		t.Helper()
		before, err := homesnap.Take(f.home)
		if err != nil {
			t.Fatal(err)
		}
		run()
		after, err := homesnap.Take(f.home)
		if err != nil {
			t.Fatal(err)
		}
		changed := homesnap.Changed(before, after)
		if inventoryProbe {
			t.Logf("%s:\n%s", name, strings.Join(changed, "\n"))
		}
		seen = append(seen, changed...)
		for _, p := range homesnap.Unlisted(changed, rows) {
			t.Errorf("%s touched %s, which is not in the make install inventory", name, p)
		}
	}
	step("first install (over a legacy panel)", func() { f.mustMake("install") })
	f.flag("active", true)
	step("upgrade", func() { f.mustMake("install") })
	unit := f.path(".config", "systemd", "user", "backstory.service")
	hypr := f.path(".config", "hypr", "backstory.lua")
	for _, p := range []string{unit, hypr} {
		b, _ := os.ReadFile(p) //nolint:gosec // fixture path under t.TempDir
		writeFixtureFile(t, p, append(bytes.Clone(b), "\n# mine\n"...))
	}
	step("upgrade over edited files", func() { f.mustMake("install") })
	for _, p := range []string{unit, hypr} {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	step("upgrade after the edits were removed", func() { f.mustMake("install") })
	units := f.path(".config", "systemd", "user")
	writeFixtureFile(t, units+"/"+install.SyncTimerName, []byte(install.SyncTimerUnit))
	writeFixtureFile(t, units+"/"+install.SyncServiceName, []byte(install.SyncServiceUnitFor(f.path(".local", "bin", "backstory"))))
	step("uninstall", func() { f.mustMake("uninstall") })
	for _, r := range homesnap.Unseen(seen, rows) {
		t.Errorf("inventory row %s was touched by no run", r.Pattern)
	}
}
