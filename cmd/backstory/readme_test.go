package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/install"
)

// readmeSection returns the body of the "## <title>" section of README.md.
func readmeSection(t *testing.T, readme, title string) string {
	t.Helper()
	i := strings.Index(readme, "\n## "+title)
	if i < 0 {
		t.Fatalf("README.md has no %q section", title)
	}
	rest := readme[i+1:]
	if j := strings.Index(rest[3:], "\n## "); j >= 0 {
		rest = rest[:j+3]
	}
	return rest
}

func TestReadmeIsCurrentWithHarnesses(t *testing.T) {
	b, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	readme := string(b)

	works := strings.ToLower(readmeSection(t, readme, "What works today"))
	for _, n := range install.HarnessNames() {
		if n == install.HarnessAgents {
			continue
		}
		if !strings.Contains(works, n) {
			t.Errorf("What works today does not mention %q", n)
		}
	}

	// Local models are a supported claim, not a promise (task cf9e7543).
	if strings.Contains(readme, "Coming for v1") {
		t.Error("README.md still has a \"Coming for v1\" section")
	}
	for _, want := range []string{"Local models", "local OpenAI-compatible provider", "model_provider"} {
		if !strings.Contains(works, strings.ToLower(want)) {
			t.Errorf("What works today lacks %q", want)
		}
	}

	inst := readmeSection(t, readme, "Install from source")
	for _, want := range []string{"backstory install bash"} {
		if !strings.Contains(inst, want) {
			t.Errorf("Install section lacks %q", want)
		}
	}
	if !regexp.MustCompile("(?m)^backstory install$").MatchString(inst) {
		t.Error("Install section lacks a bare `backstory install` line")
	}
}

// TestReadmeUninstallCommandsExist parses every `backstory <subcommand> …`
// line in README's Uninstall section and checks it against the CLI's own
// dispatch table (main.go's run) and flag sets, and checks the paths the
// section names against the constants the installer and unit use.
func TestReadmeUninstallCommandsExist(t *testing.T) {
	b, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	sec := readmeSection(t, string(b), "Uninstall")

	cmdRe := regexp.MustCompile(`(?m)^backstory (\S+)(.*)$`)
	cmds := cmdRe.FindAllStringSubmatch(sec, -1)
	if len(cmds) == 0 {
		t.Fatal("Uninstall section has no `backstory <subcommand>` commands")
	}
	removed := map[string]bool{}
	for _, m := range cmds {
		sub, rest := m[1], strings.Fields(m[2])
		// Dispatch: an unknown subcommand yields exit 2 and "unknown command".
		var out, errb strings.Builder
		run([]string{sub, "--help"}, strings.NewReader(""), &out, &errb)
		if strings.Contains(errb.String(), "unknown command") {
			t.Errorf("README names `backstory %s`, which main.go does not dispatch", sub)
			continue
		}
		if sub != "install" {
			continue
		}
		// install: positional harness names, then flags.
		for _, a := range rest {
			if strings.HasPrefix(a, "--") {
				name := strings.TrimPrefix(a, "--")
				if !strings.Contains(installUsage, "--"+name) {
					t.Errorf("README uses `install %s`, but the install CLI has no such flag", a)
				}
				if name == "remove" && len(rest) > 1 {
					removed[rest[0]] = true
				}
				continue
			}
			if a == "bash" {
				continue
			}
			known := false
			for _, n := range install.HarnessNames() {
				if n == a {
					known = true
				}
			}
			if !known {
				t.Errorf("README names harness %q, which the installer does not know", a)
			}
		}
	}
	for _, n := range []string{"claude", "codex", "hermes", "pi", "bash"} {
		if !removed[n] {
			t.Errorf("Uninstall lacks `backstory install %s --remove`", n)
		}
	}

	// Paths must match what the installer, unit and store code use.
	unit, err := os.ReadFile("../../ops/backstory.service")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unit), "ExecStart=%h/.local/bin/backstory daemon") {
		t.Error("unit ExecStart no longer %h/.local/bin/backstory")
	}
	manifest, err := os.ReadFile("../../panel/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), `"id": "backstory.this-week"`) {
		t.Error("panel manifest id is not backstory.this-week")
	}
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "/home/u")
	db, err := storePath()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"systemctl --user disable --now backstory",
		"rm ~/.config/systemd/user/backstory.service",
		"rm ~/.local/bin/backstory",
		"rm -r ~/.config/omarchy/plugins/backstory.this-week",
		"rm -r " + strings.Replace(filepath.Dir(db), "/home/u", "~", 1),
		"deletes all Backstory memory",
	} {
		if !strings.Contains(sec, want) {
			t.Errorf("Uninstall section lacks %q", want)
		}
	}
	if install.DefaultBashrcPath("/h") != "/h/.bashrc" {
		t.Error("bashrc path constant changed; README must be revisited")
	}
}
