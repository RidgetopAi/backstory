package install

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// snapshotTree reads every file under dir into a relpath -> content map.
func snapshotTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	m := map[string]string{}
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if info.IsDir() {
			m[rel+"/"] = ""
			return nil
		}
		b, err := os.ReadFile(p) //nolint:gosec // fixture path under t.TempDir
		if err != nil {
			return err
		}
		m[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestBareInstallSkipsHermesWithForeignProviderAndInstallsTheRest: claude,
// codex, pi and hermes are all present and hermes already runs another memory
// provider. The other three install, hermes is reported as a conflict, and
// not one hermes file changes.
func TestBareInstallSkipsHermesWithForeignProviderAndInstallsTheRest(t *testing.T) {
	home := t.TempDir()
	t.Setenv(HermesHomeEnv, "")
	for _, d := range []string{".claude", ".codex", ".pi/agent", ".hermes"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(home, ".hermes", "config.yaml"), "memory:\n  memory_enabled: true\n  provider: holographic\n")
	before := snapshotTree(t, filepath.Join(home, ".hermes"))

	var adapters []Adapter
	for _, d := range DetectHarnesses(home) {
		if d.Detected {
			adapters = append(adapters, d.Adapter)
		}
	}
	if len(adapters) != 4 {
		t.Fatalf("detected %d harnesses, want 4", len(adapters))
	}
	outcomes := InstallAll(adapters, home, Options{BinaryPath: testBinary, Prefix: t.TempDir()})

	got := map[string]Outcome{}
	for _, o := range outcomes {
		got[o.Adapter.Name()] = o
	}
	if len(got) != 4 {
		t.Fatalf("outcomes for %d harnesses, want 4: %v", len(got), outcomes)
	}
	for _, name := range []string{HarnessClaude, HarnessCodex, HarnessPi} {
		if got[name].Err != nil {
			t.Errorf("%s: install failed: %v", name, got[name].Err)
		}
	}
	h := got[HarnessHermes]
	if !h.Conflict || !strings.Contains(h.Err.Error(), "memory.provider") {
		t.Errorf("hermes outcome = conflict %v err %v, want a memory.provider conflict", h.Conflict, h.Err)
	}
	after := snapshotTree(t, filepath.Join(home, ".hermes"))
	if len(after) != len(before) {
		t.Errorf("hermes tree changed: before %v after %v", before, after)
	}
	for k, v := range before {
		if after[k] != v {
			t.Errorf("hermes file %s changed", k)
		}
	}
	// The three that installed really did.
	for _, p := range []string{".claude/settings.json", ".codex/hooks.json", ".pi/agent/extensions/backstory/index.ts"} {
		if _, err := os.Stat(filepath.Join(home, p)); err != nil {
			t.Errorf("%s missing: %v", p, err)
		}
	}
}

// TestHermesPluginConflictAlsoLeavesEverythingUntouched: the plugin-file
// conflict, not only the provider conflict, writes nothing (no skill either).
func TestHermesPluginConflictAlsoLeavesEverythingUntouched(t *testing.T) {
	p := hermesFixture(t, hermesFixtureYAML)
	if err := os.MkdirAll(p.PluginDir, 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(p.PluginDir, "__init__.py"), "# someone else's plugin\n")
	before := snapshotTree(t, filepath.Dir(filepath.Dir(p.PluginDir)))
	if err := InstallHermes(p, Options{Prefix: t.TempDir()}); !IsConflict(err) {
		t.Fatalf("err = %v, want a foreign-conflict", err)
	}
	after := snapshotTree(t, filepath.Dir(filepath.Dir(p.PluginDir)))
	if len(after) != len(before) {
		t.Errorf("tree changed: before %v after %v", before, after)
	}
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s changed", k)
		}
	}
}

// TestHookAndMCPEntriesUseAbsoluteBinaryPath: claude's hooks and MCP entry and
// codex's hooks and config.toml name the binary by absolute path, never the
// bare `backstory` a harness without ~/.local/bin on PATH cannot find. With no
// BinaryPath the running executable is used.
func TestHookAndMCPEntriesUseAbsoluteBinaryPath(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts Options
		want string
	}{
		{"explicit", Options{BinaryPath: testBinary, Prefix: t.TempDir()}, testBinary},
		{"default executable", Options{Prefix: t.TempDir()}, mustExecutable(t)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			if err := (claudeAdapter{}).Install(home, tc.opts); err != nil {
				t.Fatal(err)
			}
			if err := (codexAdapter{}).Install(home, tc.opts); err != nil {
				t.Fatal(err)
			}
			claudeJSON := readFile(t, filepath.Join(home, ".claude.json"))
			settings := readFile(t, filepath.Join(home, ".claude", "settings.json"))
			hooks := readFile(t, filepath.Join(home, ".codex", "hooks.json"))
			toml := readFile(t, filepath.Join(home, ".codex", "config.toml"))

			if !strings.Contains(claudeJSON, `"command": "`+tc.want+`"`) {
				t.Errorf(".claude.json mcp command is not %s:\n%s", tc.want, claudeJSON)
			}
			if !strings.Contains(toml, `command = "`+tc.want+`"`) {
				t.Errorf("config.toml mcp command is not %s:\n%s", tc.want, toml)
			}
			for label, doc := range map[string]string{"settings.json": settings, "hooks.json": hooks} {
				if !strings.Contains(doc, tc.want+" hook session-start") || !strings.Contains(doc, tc.want+" hook post-tool-use") {
					t.Errorf("%s hooks do not use %s:\n%s", label, tc.want, doc)
				}
				for _, line := range strings.Split(doc, "\n") {
					if strings.Contains(line, `"command"`) && strings.Contains(line, `"backstory `) {
						t.Errorf("%s has a bare-PATH command: %s", label, line)
					}
				}
			}
			for label, doc := range map[string]string{".claude.json": claudeJSON, "config.toml": toml} {
				if strings.Contains(doc, `"backstory"`) && strings.Contains(doc, `command": "backstory"`) {
					t.Errorf("%s has a bare mcp command", label)
				}
			}
		})
	}
}

// TestInstallUpgradesBareHookEntriesInPlace: a settings.json written by an
// older release (bare command, no compact) is rewritten, not duplicated, and
// --remove still takes out an entry written for a different binary path.
func TestInstallUpgradesBareHookEntriesInPlace(t *testing.T) {
	home := t.TempDir()
	paths := DefaultPaths(home)
	if err := os.MkdirAll(filepath.Dir(paths.SettingsJSON), 0o750); err != nil {
		t.Fatal(err)
	}
	writeFile(t, paths.SettingsJSON, `{"hooks":{"SessionStart":[{"matcher":"startup|resume|clear","hooks":[{"type":"command","command":"backstory hook session-start","timeout":10}]}]}}`)
	opts := Options{BinaryPath: testBinary, Prefix: t.TempDir()}
	if err := Install(paths, opts); err != nil {
		t.Fatal(err)
	}
	settings := readFile(t, paths.SettingsJSON)
	if n := strings.Count(settings, "hook session-start"); n != 1 {
		t.Errorf("session-start entries = %d, want 1:\n%s", n, settings)
	}
	if strings.Contains(settings, `"backstory hook session-start"`) {
		t.Errorf("bare entry survived:\n%s", settings)
	}
	if err := Remove(paths, Options{BinaryPath: "/elsewhere/backstory", Prefix: opts.Prefix}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, paths.SettingsJSON); strings.Contains(got, "backstory") {
		t.Errorf("remove left an entry written for another binary path:\n%s", got)
	}
}

// TestStubLinesCarryHandoffRuleAndMatcherIncludesCompact: every harness's
// stub line says to end with `note handoff` carrying `next` and `supersedes`,
// and the Claude SessionStart matcher re-injects the block after compaction.
func TestStubLinesCarryHandoffRuleAndMatcherIncludesCompact(t *testing.T) {
	for name, line := range map[string]string{"claude": StubLine, "codex": CodexStubLine, "agents": AgentsStubLine, "pi": PiStubLine} {
		for _, w := range []string{"note handoff", "next", "supersedes"} {
			if !strings.Contains(line, w) {
				t.Errorf("%s stub line lacks %q: %s", name, w, line)
			}
		}
	}
	found := false
	for _, m := range strings.Split(HookMatcher, "|") {
		if m == "compact" {
			found = true
		}
	}
	if !found {
		t.Errorf("HookMatcher %q lacks compact", HookMatcher)
	}
	home := t.TempDir()
	if err := Install(DefaultPaths(home), Options{BinaryPath: testBinary, Prefix: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, DefaultPaths(home).SettingsJSON); !strings.Contains(got, `"matcher": "`+HookMatcher+`"`) || !strings.Contains(HookMatcher, "compact") {
		t.Errorf("settings.json matcher lacks compact:\n%s", got)
	}
	if got := readFile(t, DefaultPaths(home).ClaudeMD); !strings.Contains(got, "note handoff") {
		t.Errorf("CLAUDE.md stub lacks the handoff rule:\n%s", got)
	}
}

// TestDaemonAnsweringDialsTheSocket: not answering with no listener (even
// with a stale socket file), answering when a test daemon listens, with and
// without XDG_RUNTIME_DIR.
func TestDaemonAnsweringDialsTheSocket(t *testing.T) {
	for _, withXDG := range []bool{true, false} {
		name := "without XDG_RUNTIME_DIR"
		if withXDG {
			name = "with XDG_RUNTIME_DIR"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			base := filepath.Join(home, ".local", "state")
			if withXDG {
				base = t.TempDir()
				t.Setenv("XDG_RUNTIME_DIR", base)
			} else {
				t.Setenv("XDG_RUNTIME_DIR", "")
			}
			sock := filepath.Join(base, "backstory", "sock")
			if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
				t.Fatal(err)
			}

			if path, ok := DaemonAnswering(Options{}); ok || path != sock {
				t.Fatalf("no listener: path %q answering %v, want %q false", path, ok, sock)
			}
			ln, err := net.Listen("unix", sock)
			if err != nil {
				t.Fatal(err)
			}
			if path, ok := DaemonAnswering(Options{}); !ok || path != sock {
				t.Fatalf("listener up: path %q answering %v, want %q true", path, ok, sock)
			}
			_ = ln.Close() // leaves the stale socket file behind
			if _, ok := DaemonAnswering(Options{}); ok {
				t.Fatal("stale socket file with no listener reads as answering")
			}
		})
	}
}

func mustExecutable(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

// TestStubLinesSayWhereHandoffGuidanceGoes: the skill is rarely loaded, so
// the stub line itself must say what goes in `next` versus the handoff text.
func TestStubLinesSayWhereHandoffGuidanceGoes(t *testing.T) {
	for name, line := range map[string]string{"claude": StubLine, "codex": CodexStubLine, "agents": AgentsStubLine, "pi": PiStubLine} {
		for _, w := range []string{"verify", "next", "conditions", "belong in the handoff text"} {
			if !strings.Contains(line, w) {
				t.Errorf("%s stub line lacks %q: %s", name, w, line)
			}
		}
	}
}
