// Adapter seam: `backstory install <harness>` routes through one Adapter
// per harness (decision 3e14db82's v1 list: Claude Code, Codex, Hermes, Pi,
// and "agents" — the AGENTS.md-based catch-all for the rest). All five are
// real adapters.
package install

import "errors"

// Harness names backstory install recognizes, in the order Adapters and
// HarnessNames report them.
const (
	HarnessClaude = "claude"
	HarnessCodex  = "codex"
	HarnessHermes = "hermes"
	HarnessPi     = "pi"
	HarnessAgents = "agents"
)

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
// the order used for enumeration and error messages: claude (the default)
// first, then the rest of decision 3e14db82's v1 list.
func Adapters() []Adapter {
	return []Adapter{
		claudeAdapter{},
		codexAdapter{},
		hermesAdapter{},
		piAdapter{},
		agentsAdapter{},
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

// AdapterByName looks up a harness by name. The bool is false when the name
// is not recognized.
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

// Outcome is one adapter's result from InstallAll.
type Outcome struct {
	Adapter Adapter
	// Err is nil when the harness was installed.
	Err error
	// Conflict is true when Err is a foreign-conflict: the harness was left
	// exactly as it was and the rest of the run carried on.
	Conflict bool
}

// IsConflict reports whether err is a foreign-conflict (something else
// already occupies a slot backstory would write).
func IsConflict(err error) bool {
	return errors.Is(err, ErrForeignConflict) || errors.Is(err, ErrPiExtensionForeign)
}

// InstallAll installs every adapter in order and never stops early: a
// conflict in one harness is recorded and the next is still installed. The
// adapters check for conflicts before writing, so a conflicted harness is
// untouched.
func InstallAll(adapters []Adapter, home string, opts Options) []Outcome {
	out := make([]Outcome, 0, len(adapters))
	for _, a := range adapters {
		err := a.Install(home, opts)
		out = append(out, Outcome{Adapter: a, Err: err, Conflict: IsConflict(err)})
	}
	return out
}
