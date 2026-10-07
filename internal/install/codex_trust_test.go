package install

import (
	"fmt"
	"strings"
	"testing"
)

const codexUserHooksState = `[hooks.state."/somewhere/else/hooks.json:session_start:0:0"]
trusted_hash = "sha256:userowned"
`

func codexHookStatuses(t *testing.T, p CodexPaths) map[string]ItemStatus {
	t.Helper()
	items, err := CheckCodex(p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]ItemStatus{}
	for _, it := range items {
		m[it.Name] = it.Status
	}
	return m
}

func trustEntries(p CodexPaths) string {
	var b strings.Builder
	for i, ev := range []string{codexEventSessionStart, codexEventPostToolUse} {
		_ = i
		fmt.Fprintf(&b, "[hooks.state.\"%s:%s:0:0\"]\ntrusted_hash = \"sha256:abc\"\n\n", p.HooksJSON, ev)
	}
	return b.String()
}

// Clause 1: no trust entries -> hooks are not-trusted, never plain present.
func TestCodexCheckReportsUntrustedHooks(t *testing.T) {
	p := codexFixture(t, codexFixtureTOML)
	if err := InstallCodex(p, Options{}); err != nil {
		t.Fatal(err)
	}
	st := codexHookStatuses(t, p)
	for _, n := range []string{ItemSessionStartHook, ItemPostToolUseHook} {
		if st[n] != StatusNotTrusted {
			t.Errorf("%s = %s, want %s", n, st[n], StatusNotTrusted)
		}
	}
	// A trust entry for some other hooks.json must not count.
	writeFile(t, p.ConfigTOML, readFile(t, p.ConfigTOML)+"\n"+codexUserHooksState)
	if st := codexHookStatuses(t, p); st[ItemSessionStartHook] != StatusNotTrusted {
		t.Errorf("foreign trust entry satisfied check: %s", st[ItemSessionStartHook])
	}
}

// Clause 2 (path ii): install names the approval step; --check is
// not-trusted until a matching entry exists, then present.
func TestCodexInstallNoticeAndTrustTransition(t *testing.T) {
	if n := (codexAdapter{}).InstallNotice(t.TempDir(), Options{}); strings.Count(n, "\n") != 0 || !strings.Contains(n, "approve the Backstory hooks") {
		t.Errorf("notice = %q", n)
	}
	p := codexFixture(t, codexFixtureTOML)
	if err := InstallCodex(p, Options{}); err != nil {
		t.Fatal(err)
	}
	if st := codexHookStatuses(t, p); st[ItemSessionStartHook] != StatusNotTrusted {
		t.Fatalf("before approval: %s", st[ItemSessionStartHook])
	}
	writeFile(t, p.ConfigTOML, readFile(t, p.ConfigTOML)+"\n"+trustEntries(p))
	st := codexHookStatuses(t, p)
	for _, n := range []string{ItemSessionStartHook, ItemPostToolUseHook} {
		if st[n] != StatusPresent {
			t.Errorf("after approval %s = %s", n, st[n])
		}
	}
	// Empty trusted_hash does not count.
	writeFile(t, p.ConfigTOML, codexFixtureTOML+"\n"+strings.ReplaceAll(trustEntries(p), "sha256:abc", ""))
	if st := codexHookStatuses(t, p); st[ItemSessionStartHook] != StatusNotTrusted {
		t.Errorf("empty hash trusted: %s", st[ItemSessionStartHook])
	}
}

// Clause 3: install then remove leaves a user's config.toml (including a
// hooks.state entry of their own, and trust entries codex wrote) untouched.
func TestCodexRoundTripPreservesUserTrustState(t *testing.T) {
	p := codexFixture(t, "")
	seed := codexFixtureTOML + "\n" + codexUserHooksState + "\n" + trustEntries(p)
	writeFile(t, p.ConfigTOML, seed)
	if err := InstallCodex(p, Options{}); err != nil {
		t.Fatal(err)
	}
	if st := codexHookStatuses(t, p); st[ItemSessionStartHook] != StatusPresent {
		t.Errorf("approved hook = %s", st[ItemSessionStartHook])
	}
	if err := RemoveCodex(p, Options{}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, p.ConfigTOML); got != seed {
		t.Errorf("config.toml changed:\n%s\nwant:\n%s", got, seed)
	}
}

// Option A: Backstory never writes Codex's hooks.state trust entries.
func TestInstallCodexNeverWritesHooksState(t *testing.T) {
	for name, toml := range map[string]string{"fresh": "", "existing": codexFixtureTOML} {
		p := codexFixture(t, toml)
		if err := InstallCodex(p, Options{}); err != nil {
			t.Fatal(err)
		}
		if err := InstallCodex(p, Options{}); err != nil {
			t.Fatal(err)
		}
		cfg := readFile(t, p.ConfigTOML)
		if strings.Contains(cfg, "hooks.state") || strings.Contains(cfg, "trusted_hash") {
			t.Errorf("%s: install codex wrote trust state:\n%s", name, cfg)
		}
	}
}
