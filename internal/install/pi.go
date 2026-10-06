package install

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/RidgetopAi/backstory/internal/skill"
)

// Pi item names, for --check output.
const (
	ItemPiExtension = "pi-extension"
	ItemPiAgentsMD  = "pi-agents-md-stub"
	ItemPiSkill     = "pi-skill"
)

// PiExtensionMarker is the line every generated extension carries, so
// install can tell its own file from a foreign index.ts at the same path.
const PiExtensionMarker = "// Backstory's Pi extension, written by `backstory install pi`"

// PiStubLine is the one-line stub added to ~/.pi/agent/AGENTS.md. Pi gets the
// warm block by injection (the extension), so the stub names the tools and
// points at the backstory skill, as the Claude stub does.
const PiStubLine = "Backstory: a warm block is injected at session start; for more, call the `recall`, `note`, `timeline`, `confirm` and `status` tools; see the `backstory` skill, `~/.pi/agent/skills/backstory/SKILL.md`." + HandoffClause

var piStubBlock = StubMarkerBegin + "\n" + PiStubLine + "\n" + StubMarkerEnd + "\n"

//go:embed piext/index.ts
var piExtensionSource []byte

// PiExtensionSource returns the embedded extension TypeScript.
func PiExtensionSource() []byte { return append([]byte(nil), piExtensionSource...) }

// ErrPiExtensionForeign is returned when ~/.pi/agent/extensions/backstory/index.ts
// exists but was not written by backstory; install never overwrites it.
var ErrPiExtensionForeign = errors.New("install: ~/.pi/agent/extensions/backstory/index.ts exists and is not backstory's")

// PiPaths locates every file the Pi adapter touches under one $HOME.
type PiPaths struct {
	ExtensionDir string // ~/.pi/agent/extensions/backstory
	ExtensionTS  string // .../index.ts
	AgentsMD     string // ~/.pi/agent/AGENTS.md
	SkillPath    string // ~/.pi/agent/skills/backstory/SKILL.md
}

// DefaultPiPaths returns Pi's user-level surfaces under home.
func DefaultPiPaths(home string) PiPaths {
	agent := filepath.Join(home, ".pi", "agent")
	dir := filepath.Join(agent, "extensions", "backstory")
	return PiPaths{ExtensionDir: dir, ExtensionTS: filepath.Join(dir, "index.ts"), AgentsMD: filepath.Join(agent, "AGENTS.md"), SkillPath: filepath.Join(agent, "skills", "backstory", "SKILL.md")}
}

type piAdapter struct{}

func (piAdapter) Name() string { return HarnessPi }

func (piAdapter) Install(home string, opts Options) error {
	p := DefaultPiPaths(home)
	status, err := piExtensionStatus(p.ExtensionTS)
	if err != nil {
		return err
	}
	if status == StatusForeign {
		return ErrPiExtensionForeign
	}
	if status != StatusPresent {
		if err := os.MkdirAll(p.ExtensionDir, 0o750); err != nil {
			return fmt.Errorf("%s: %w", ItemPiExtension, err)
		}
		if err := writeAtomic(p.ExtensionTS, piExtensionSource, 0o644); err != nil {
			return fmt.Errorf("%s: %w", ItemPiExtension, err)
		}
	}
	if _, err := skill.Install(p.SkillPath, opts.Prefix); err != nil {
		return fmt.Errorf("%s: %w", ItemPiSkill, err)
	}
	if err := installStubBlock(p.AgentsMD, piStubBlock); err != nil {
		return fmt.Errorf("%s: %w", ItemPiAgentsMD, err)
	}
	return nil
}

// Remove deletes the extension (and its directory, and any parent directory
// install created, once empty) and the AGENTS.md stub, restoring pre-install
// bytes. A foreign index.ts is left alone.
func (piAdapter) Remove(home string, opts Options) error {
	p := DefaultPiPaths(home)
	if err := skill.Remove(p.SkillPath, opts.Prefix); err != nil {
		return fmt.Errorf("%s: %w", ItemPiSkill, err)
	}
	// Prune the skill directories install created, stopping at the first one in use.
	for d := filepath.Dir(p.SkillPath); d != filepath.Dir(p.ExtensionDir); d = filepath.Dir(d) {
		if os.Remove(d) != nil {
			break
		}
	}
	status, err := piExtensionStatus(p.ExtensionTS)
	if err != nil {
		return err
	}
	if status != StatusForeign && status != StatusAbsent {
		if err := os.Remove(p.ExtensionTS); err != nil {
			return fmt.Errorf("%s: %w", ItemPiExtension, err)
		}
	}
	// os.Remove on a non-empty directory fails, so unrelated files stay.
	// Stop at the first directory that is still in use.
	extensions := filepath.Dir(p.ExtensionDir)
	agent := filepath.Dir(extensions)
	for _, d := range []string{p.ExtensionDir, extensions, agent, filepath.Dir(agent)} {
		if os.Remove(d) != nil {
			break
		}
	}
	if err := removeStub(p.AgentsMD); err != nil {
		return fmt.Errorf("%s: %w", ItemPiAgentsMD, err)
	}
	for _, d := range []string{agent, filepath.Dir(agent)} {
		if os.Remove(d) != nil {
			break
		}
	}
	return nil
}

func (piAdapter) Check(home string, opts Options) ([]Item, error) {
	p := DefaultPiPaths(home)
	ext, err := piExtensionStatus(p.ExtensionTS)
	if err != nil {
		return nil, err
	}
	stub := StatusAbsent
	if data, err := os.ReadFile(p.AgentsMD); err == nil && strings.Contains(string(data), piStubBlock) { //nolint:gosec // path derived from $HOME
		stub = StatusPresent
	}
	return []Item{{Name: ItemPiExtension, Status: ext}, {Name: ItemPiAgentsMD, Status: stub}, {Name: ItemPiSkill, Status: ItemStatus(skill.CheckStatus(p.SkillPath, opts.Prefix))}}, nil
}

// piExtensionStatus: present when the file equals the embedded source,
// absent when missing, and foreign when it lacks backstory's marker. A file
// with the marker but different bytes (an older backstory) reports absent so
// install refreshes it.
func piExtensionStatus(path string) (ItemStatus, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path derived from $HOME
	if os.IsNotExist(err) {
		return StatusAbsent, nil
	}
	if err != nil {
		return "", err
	}
	if bytes.Equal(data, piExtensionSource) {
		return StatusPresent, nil
	}
	if bytes.Contains(data, []byte(PiExtensionMarker)) {
		return StatusAbsent, nil
	}
	return StatusForeign, nil
}
