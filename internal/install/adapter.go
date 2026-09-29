// Adapter seam: `backstory install <harness>` routes through one Adapter
// per harness (decision 3e14db82's v1 list: Claude Code, Codex, Hermes, Pi,
// and "agents" — the AGENTS.md-based catch-all for the rest). Only claude
// is a real adapter today; codex, hermes, pi and agents are stubs so the
// harness list stays honest until their own punches land.
package install

import (
	"errors"
	"fmt"
)

// Harness names backstory install recognizes, in the order Adapters and
// HarnessNames report them.
const (
	HarnessClaude = "claude"
	HarnessCodex  = "codex"
	HarnessHermes = "hermes"
	HarnessPi     = "pi"
	HarnessAgents = "agents"
)

// ErrAdapterNotAvailable is returned by a stub adapter's Install, Remove and
// Check: the harness name is recognized but its adapter has not landed yet
// (a separate punch per decision 3e14db82 replaces the stub).
var ErrAdapterNotAvailable = errors.New("adapter not available yet")

// Adapter installs, removes, and reports the status of Backstory's
// integration for one harness under a $HOME. Every method is idempotent (a
// repeat call changes nothing once installed) and reports a
// foreign-conflict through Check's StatusForeign rather than overwriting
// something backstory did not add.
type Adapter interface {
	// Name is the adapter's harness identifier, e.g. "claude".
	Name() string
	// Install registers the integration under home. A second call with the
	// same inputs changes zero bytes.
	Install(home string, opts Options) error
	// Remove reverses Install, leaving foreign entries untouched.
	Remove(home string, opts Options) error
	// Check reports every item's status. It never writes.
	Check(home string, opts Options) ([]Item, error)
}

// Adapters returns every harness adapter backstory install knows about, in
// the order used for enumeration and error messages: the real claude
// adapter first, then the not-yet-available stubs for the rest of decision
// 3e14db82's v1 list. codex, hermes, pi and agents become real adapters in
// their own punches, each replacing its stub here.
func Adapters() []Adapter {
	return []Adapter{
		claudeAdapter{},
		stubAdapter{name: HarnessCodex},
		stubAdapter{name: HarnessHermes},
		stubAdapter{name: HarnessPi},
		stubAdapter{name: HarnessAgents},
	}
}

// HarnessNames returns every recognized harness name, in Adapters' order.
func HarnessNames() []string {
	adapters := Adapters()
	names := make([]string, len(adapters))
	for i, a := range adapters {
		names[i] = a.Name()
	}
	return names
}

// AdapterByName looks up a harness by name. The bool is false only when the
// name is not recognized at all; a recognized-but-not-yet-available harness
// (codex, hermes, pi, agents today) still returns its stub, whose methods
// themselves fail with ErrAdapterNotAvailable.
func AdapterByName(name string) (Adapter, bool) {
	for _, a := range Adapters() {
		if a.Name() == name {
			return a, true
		}
	}
	return nil, false
}

// claudeAdapter adapts the package's original Install/Remove/Check — in
// place since before this adapter seam existed — to the Adapter interface.
// Behaviour is unchanged: DefaultPaths(home) is the same Paths every caller
// used to resolve by hand.
type claudeAdapter struct{}

func (claudeAdapter) Name() string { return HarnessClaude }

func (claudeAdapter) Install(home string, opts Options) error {
	return Install(DefaultPaths(home), opts)
}

func (claudeAdapter) Remove(home string, opts Options) error {
	return Remove(DefaultPaths(home), opts)
}

func (claudeAdapter) Check(home string, opts Options) ([]Item, error) {
	return Check(DefaultPaths(home), opts)
}

// stubAdapter is a placeholder for a harness decision 3e14db82 promises for
// v1 but whose adapter has not landed yet. Every method fails with
// ErrAdapterNotAvailable so `backstory install <name>` stays honest:
// recognized, but not yet installable.
type stubAdapter struct{ name string }

func (s stubAdapter) Name() string { return s.name }

func (s stubAdapter) Install(string, Options) error { return s.err() }

func (s stubAdapter) Remove(string, Options) error { return s.err() }

func (s stubAdapter) Check(string, Options) ([]Item, error) { return nil, s.err() }

func (s stubAdapter) err() error {
	return fmt.Errorf("%s: %w", s.name, ErrAdapterNotAvailable)
}
