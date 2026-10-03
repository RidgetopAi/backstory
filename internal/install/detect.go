package install

import (
	"os"
	"path/filepath"
	"strings"
)

// Detection is one auto-detectable harness's presence verdict under a $HOME.
type Detection struct {
	Adapter  Adapter
	Detected bool
	// Probed lists the paths looked for, in order; when Detected is false
	// none of them exists.
	Probed []string
}

// NotFound renders the paths that were not found, e.g. "~/.codex" or
// "/h/.claude or /h/.claude.json".
func (d Detection) NotFound() string { return strings.Join(d.Probed, " or ") }

// DetectHarnesses reports, for every harness except the generic agents
// fallback (which is only ever selected by name), whether its home is
// present under home. It reads the filesystem only — no PATH lookup — so
// the answer is the same in a loop, in CI and on a desktop. Hermes follows
// DefaultHermesPaths: $HERMES_HOME if set, else home/.hermes.
func DetectHarnesses(home string) []Detection {
	hermesHome := os.Getenv(HermesHomeEnv)
	if hermesHome == "" {
		hermesHome = filepath.Join(home, ".hermes")
	}
	probes := []struct {
		name   string
		probed []string
	}{
		{HarnessClaude, []string{filepath.Join(home, ".claude"), filepath.Join(home, ".claude.json")}},
		{HarnessCodex, []string{filepath.Join(home, ".codex")}},
		{HarnessHermes, []string{hermesHome}},
		{HarnessPi, []string{filepath.Join(home, ".pi", "agent")}},
	}
	out := make([]Detection, 0, len(probes))
	for _, p := range probes {
		a, ok := AdapterByName(p.name)
		if !ok {
			continue
		}
		d := Detection{Adapter: a, Probed: p.probed}
		for _, path := range p.probed {
			if _, err := os.Stat(path); err == nil {
				d.Detected = true
				break
			}
		}
		out = append(out, d)
	}
	return out
}
