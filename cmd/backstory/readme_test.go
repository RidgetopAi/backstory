package main

import (
	"os"
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

	coming := readmeSection(t, readme, "Coming for v1")
	for _, n := range []string{"codex", "hermes", "pi"} {
		if regexp.MustCompile(`(?i)\b` + n + `\b`).MatchString(coming) {
			t.Errorf("Coming for v1 still lists %q", n)
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
