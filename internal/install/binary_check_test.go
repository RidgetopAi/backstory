package install

import (
	"strings"
	"testing"
)

// Install codex prints the approval line only while the hooks are untrusted.
func TestCodexInstallNoticeOnlyWhenHooksUntrusted(t *testing.T) {
	a := codexAdapter{}

	// No trust entries: install, then the notice names the approval step.
	p := codexFixture(t, codexFixtureTOML)
	home := strings.TrimSuffix(p.ConfigTOML, "/.codex/config.toml")
	if err := a.Install(home, Options{}); err != nil {
		t.Fatal(err)
	}
	if n := a.InstallNotice(home, Options{}); n != CodexTrustNotice {
		t.Errorf("untrusted: notice = %q, want the approval line", n)
	}

	// Trust entries already in config.toml: re-install prints nothing.
	writeFile(t, p.ConfigTOML, readFile(t, p.ConfigTOML)+"\n"+trustEntries(p))
	if err := a.Install(home, Options{}); err != nil {
		t.Fatal(err)
	}
	if n := a.InstallNotice(home, Options{}); n != "" {
		t.Errorf("trusted: notice = %q, want none", n)
	}
}

// --check without --binary recognises entries an install made with --binary.
func TestCheckRecognisesInstallMadeWithBinary(t *testing.T) {
	const fixtureBin = "/opt/fixture dir/bs-custom"
	home := t.TempDir()
	withBin := Options{BinaryPath: fixtureBin}
	for _, a := range []Adapter{claudeAdapter{}, codexAdapter{}} {
		if err := a.Install(home, withBin); err != nil {
			t.Fatalf("%s install: %v", a.Name(), err)
		}
	}
	p := DefaultCodexPaths(home)
	writeFile(t, p.ConfigTOML, readFile(t, p.ConfigTOML)+"\n"+trustEntries(p))

	for _, a := range []Adapter{claudeAdapter{}, codexAdapter{}} {
		items, err := a.Check(home, Options{BinaryPath: "/somewhere/else/backstory"})
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			isBinItem := it.Name == ItemMCPServer || it.Name == ItemCodexMCPServer ||
				it.Name == ItemSessionStartHook || it.Name == ItemPostToolUseHook || it.Name == ItemPostToolUseFailureHook
			if it.Status != StatusPresent {
				t.Errorf("%s %s = %s, want present", a.Name(), it.Name, it.Status)
			}
			if isBinItem && it.Binary != fixtureBin {
				t.Errorf("%s %s binary = %q, want %q", a.Name(), it.Name, it.Binary, fixtureBin)
			}
		}
	}
}
