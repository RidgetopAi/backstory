// Hermes adapter (decision 3e14db82): `backstory install hermes` writes
// Backstory as a Hermes Agent memory provider — the plugin under
// $HERMES_HOME/plugins/backstory/ (plugin.yaml + __init__.py) and
// `memory.provider: backstory` in $HERMES_HOME/config.yaml.
//
// A provider (not shell hooks) is used so Hermes never shows its first-use
// hook consent prompt; the installer therefore never writes
// hooks_auto_accept or the shell-hooks allowlist. Only one external memory
// provider runs at a time, so a config.yaml whose memory.provider names
// anything else is a foreign-conflict: reported, never overwritten.
//
// config.yaml is hand-written, so it is never parsed and re-serialized: the
// one key is edited as text and every edit carries a marker comment from
// which remove restores the exact pre-install bytes.
package install

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/RidgetopAi/backstory/internal/mcp"
)

// Hermes item names, for --check output.
const (
	ItemHermesPlugin         = "memory-plugin"
	ItemHermesMemoryProvider = "memory-provider"
)

// HermesProviderName is the value memory.provider takes.
const HermesProviderName = "backstory"

// HermesHomeEnv is Hermes' own home override; unset means ~/.hermes.
const HermesHomeEnv = "HERMES_HOME"

// Markers. The plugin files carry hermesManagedMarker on their first line;
// config.yaml edits carry a "# backstory:" trailer (or a begin/end block).
const (
	hermesManagedMarker = "# managed by `backstory install hermes`; do not edit"
	hermesToolsToken    = "__BACKSTORY_TOOLS_JSON__"

	hermesAddedTrailer  = "  # backstory:added"
	hermesOrigTrailer   = "  # backstory:orig="
	hermesBlockBegin    = "# backstory:begin (managed by `backstory install hermes`; do not edit)"
	hermesBlockEnd      = "# backstory:end"
	hermesNoEOLFlag     = " [no-eol]"
	hermesDefaultIndent = "  "
)

//go:embed hermes_plugin/plugin.yaml
var hermesPluginYAML string

//go:embed hermes_plugin/__init__.py
var hermesPluginPy string

// HermesPaths locates the files the Hermes adapter touches.
type HermesPaths struct {
	ConfigYAML string
	PluginDir  string
}

// DefaultHermesPaths resolves $HERMES_HOME, else home/.hermes.
func DefaultHermesPaths(home string) HermesPaths {
	dir := os.Getenv(HermesHomeEnv)
	if dir == "" {
		dir = filepath.Join(home, ".hermes")
	}
	return HermesPathsIn(dir)
}

// HermesPathsIn locates the Hermes surfaces under a Hermes home directory.
func HermesPathsIn(hermesHome string) HermesPaths {
	return HermesPaths{
		ConfigYAML: filepath.Join(hermesHome, "config.yaml"),
		PluginDir:  filepath.Join(hermesHome, "plugins", HermesProviderName),
	}
}

// HermesPluginFiles returns the plugin's files by name, exactly as install
// writes them: __init__.py has the five tool schemas (from mcp.ToolsV0, the
// same source `backstory mcp` serves) rendered in.
func HermesPluginFiles() (map[string]string, error) {
	tools := mcp.ToolsV0()
	schemas := make([]map[string]any, len(tools))
	for i, t := range tools {
		schemas[i] = map[string]any{"name": t.Name, "description": t.Description, "parameters": t.InputSchema}
	}
	raw, err := json.Marshal(schemas)
	if err != nil {
		return nil, err
	}
	if strings.Contains(string(raw), "'''") {
		return nil, fmt.Errorf("tool schemas contain a Python triple quote")
	}
	return map[string]string{
		"plugin.yaml": hermesPluginYAML,
		"__init__.py": strings.Replace(hermesPluginPy, hermesToolsToken, string(raw), 1),
	}, nil
}

type hermesAdapter struct{}

func (hermesAdapter) Name() string { return HarnessHermes }

func (hermesAdapter) Install(home string, opts Options) error {
	return InstallHermes(DefaultHermesPaths(home), opts)
}

func (hermesAdapter) Remove(home string, opts Options) error {
	return RemoveHermes(DefaultHermesPaths(home), opts)
}

func (hermesAdapter) Check(home string, opts Options) ([]Item, error) {
	return CheckHermes(DefaultHermesPaths(home), opts)
}

// InstallHermes installs the plugin files, then sets memory.provider. A
// foreign plugin file or a foreign memory.provider is reported as an
// ErrForeignConflict naming the item and left untouched; the other item is
// still installed.
func InstallHermes(paths HermesPaths, _ Options) error {
	var conflicts []string
	if err := installHermesPlugin(paths.PluginDir); err != nil {
		if !isForeign(err) {
			return fmt.Errorf("%s: %w", ItemHermesPlugin, err)
		}
		conflicts = append(conflicts, err.Error())
	}
	if err := installHermesConfig(paths.ConfigYAML); err != nil {
		if !isForeign(err) {
			return fmt.Errorf("%s: %w", ItemHermesMemoryProvider, err)
		}
		conflicts = append(conflicts, err.Error())
	}
	if len(conflicts) > 0 {
		return fmt.Errorf("%w: %s", ErrForeignConflict, strings.Join(conflicts, "; "))
	}
	return nil
}

func isForeign(err error) bool { return errors.Is(err, ErrForeignConflict) }

// RemoveHermes reverses InstallHermes, restoring pre-install config bytes.
func RemoveHermes(paths HermesPaths, _ Options) error {
	if err := removeHermesPlugin(paths.PluginDir); err != nil {
		return fmt.Errorf("%s: %w", ItemHermesPlugin, err)
	}
	if err := removeHermesConfig(paths.ConfigYAML); err != nil {
		return fmt.Errorf("%s: %w", ItemHermesMemoryProvider, err)
	}
	return nil
}

// CheckHermes reports both items without writing.
func CheckHermes(paths HermesPaths, _ Options) ([]Item, error) {
	return []Item{
		{Name: ItemHermesPlugin, Status: hermesPluginStatus(paths.PluginDir)},
		{Name: ItemHermesMemoryProvider, Status: hermesConfigStatus(paths.ConfigYAML)},
	}, nil
}

// --- plugin files ---

func isOurs(content string) bool {
	return strings.HasPrefix(content, hermesManagedMarker+"\n")
}

func installHermesPlugin(dir string) error {
	files, err := HermesPluginFiles()
	if err != nil {
		return err
	}
	// Foreign check first, so a conflict writes nothing at all.
	for name := range files {
		data, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // caller-chosen plugin location
		if err == nil && !isOurs(string(data)) {
			return fmt.Errorf("%w: %s: %s exists and was not written by backstory; left untouched", ErrForeignConflict, ItemHermesPlugin, filepath.Join(dir, name))
		}
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	for name, want := range files {
		p := filepath.Join(dir, name)
		if data, err := os.ReadFile(p); err == nil && string(data) == want { //nolint:gosec // caller-chosen plugin location
			continue
		}
		if err := writeAtomic(p, []byte(want), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func removeHermesPlugin(dir string) error {
	files, err := HermesPluginFiles()
	if err != nil {
		return err
	}
	for name := range files {
		p := filepath.Join(dir, name)
		data, err := os.ReadFile(p) //nolint:gosec // caller-chosen plugin location
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !isOurs(string(data)) {
			continue
		}
		if err := os.Remove(p); err != nil {
			return err
		}
	}
	// Hermes byte-compiles the plugin on load; that cache is ours to clear.
	// Empty directories are pruned; a non-empty one (foreign files) is kept.
	if _, err := os.Stat(filepath.Join(dir, "__init__.py")); os.IsNotExist(err) {
		_ = os.RemoveAll(filepath.Join(dir, "__pycache__"))
	}
	if err := os.Remove(dir); err == nil {
		_ = os.Remove(filepath.Dir(dir))
	}
	return nil
}

func hermesPluginStatus(dir string) ItemStatus {
	files, err := HermesPluginFiles()
	if err != nil {
		return StatusAbsent
	}
	status := StatusPresent
	for name, want := range files {
		data, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // caller-chosen plugin location
		switch {
		case err != nil:
			if status == StatusPresent {
				status = StatusAbsent
			}
		case !isOurs(string(data)):
			return StatusForeign
		case string(data) != want:
			status = StatusAbsent // ours but stale: install would rewrite it
		}
	}
	return status
}

// --- config.yaml: memory.provider ---

// hermesProviderState is what a config.yaml says about memory.provider.
type hermesProviderState int

const (
	provMissingMemory hermesProviderState = iota // no top-level memory key
	provMissingKey                               // memory block without a provider key
	provEmpty                                    // provider key present, value empty/null
	provOurs                                     // provider: backstory
	provForeign                                  // any other value, or a shape we will not edit
)

type hermesScan struct {
	state       hermesProviderState
	memoryLine  int    // index of the `memory:` line, when present
	keyLine     int    // index of the provider line, when present
	childIndent string // indentation of the memory block's children
	block       bool   // our begin/end block is present
}

func yamlIndent(line string) int { return len(line) - len(strings.TrimLeft(line, " ")) }

func yamlBlank(line string) bool {
	t := strings.TrimSpace(line)
	return t == "" || strings.HasPrefix(t, "#")
}

// yamlScalar strips a trailing comment and quotes from a value.
func yamlScalar(v string) string {
	v = strings.TrimSpace(v)
	if len(v) > 0 && (v[0] == '\'' || v[0] == '"') {
		q := v[0]
		if end := strings.IndexByte(v[1:], q); end >= 0 {
			return v[1 : 1+end]
		}
		return v
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	if strings.HasPrefix(v, "#") {
		return ""
	}
	return strings.TrimSpace(v)
}

func yamlKeyValue(line, key string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimLeft(line, " "), key)
	if !ok {
		return "", false
	}
	rest = strings.TrimLeft(rest, " ")
	v, ok := strings.CutPrefix(rest, ":")
	if !ok {
		return "", false
	}
	if v != "" && v[0] != ' ' && v[0] != '\t' && v[0] != '\r' {
		return "", false
	}
	return v, true
}

func scanHermesConfig(content string) hermesScan {
	lines := strings.Split(content, "\n")
	s := hermesScan{state: provMissingMemory, childIndent: hermesDefaultIndent}
	for i, l := range lines {
		if strings.HasPrefix(l, hermesBlockBegin) {
			s.block = true
		}
		if yamlIndent(l) != 0 || yamlBlank(l) {
			continue
		}
		v, ok := yamlKeyValue(l, "memory")
		if !ok {
			continue
		}
		s.memoryLine = i
		switch sc := yamlScalar(v); sc {
		case "", "null", "~":
		default:
			s.state = provForeign // flow mapping or scalar: not minimally editable
			return s
		}
		s.state = provMissingKey
		childIndent := -1
		for j := i + 1; j < len(lines); j++ {
			c := lines[j]
			if yamlBlank(c) {
				continue
			}
			ind := yamlIndent(c)
			if ind == 0 {
				break
			}
			if childIndent == -1 {
				childIndent = ind
				s.childIndent = strings.Repeat(" ", ind)
			}
			if ind != childIndent {
				continue
			}
			pv, ok := yamlKeyValue(c, "provider")
			if !ok {
				continue
			}
			s.keyLine = j
			switch p := yamlScalar(pv); p {
			case "", "null", "~":
				s.state = provEmpty
			case HermesProviderName:
				s.state = provOurs
			default:
				s.state = provForeign
			}
			return s
		}
		return s
	}
	return s
}

func hermesBlock(noEOL bool) string {
	begin := hermesBlockBegin
	if noEOL {
		begin += hermesNoEOLFlag
	}
	return begin + "\nmemory:\n" + hermesDefaultIndent + "provider: " + HermesProviderName + "\n" + hermesBlockEnd + "\n"
}

func readOptional(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // caller-chosen config location
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return string(data), nil
}

func installHermesConfig(path string) error {
	content, err := readOptional(path)
	if err != nil {
		return err
	}
	s := scanHermesConfig(content)
	switch s.state {
	case provOurs:
		return nil
	case provForeign:
		return fmt.Errorf("%w: %s: %s sets memory.provider to something other than %s (only one memory provider runs at a time); left untouched", ErrForeignConflict, ItemHermesMemoryProvider, path, HermesProviderName)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	lines := strings.Split(content, "\n")
	switch s.state {
	case provEmpty:
		orig := lines[s.keyLine]
		cr := ""
		if strings.HasSuffix(orig, "\r") {
			orig, cr = strings.TrimSuffix(orig, "\r"), "\r"
		}
		lines[s.keyLine] = s.childIndent + "provider: " + HermesProviderName + hermesOrigTrailer + strconv.Quote(orig) + cr
	case provMissingKey:
		add := s.childIndent + "provider: " + HermesProviderName + hermesAddedTrailer
		if strings.HasSuffix(lines[s.memoryLine], "\r") {
			add += "\r"
		}
		lines = append(lines[:s.memoryLine+1], append([]string{add}, lines[s.memoryLine+1:]...)...)
	case provMissingMemory:
		noEOL := content != "" && !strings.HasSuffix(content, "\n")
		if noEOL {
			content += "\n"
		}
		return writeAtomic(path, []byte(content+hermesBlock(noEOL)), fileMode(path))
	}
	return writeAtomic(path, []byte(strings.Join(lines, "\n")), fileMode(path))
}

func removeHermesConfig(path string) error {
	content, err := readOptional(path)
	if err != nil {
		return err
	}
	if content == "" {
		return nil
	}
	if b := strings.Index(content, hermesBlockBegin); b >= 0 {
		rel := strings.Index(content[b:], "\n"+hermesBlockEnd)
		if rel < 0 {
			return fmt.Errorf("%s: found %q without %q", path, hermesBlockBegin, hermesBlockEnd)
		}
		end := b + rel + 1 + len(hermesBlockEnd)
		if end < len(content) && content[end] == '\n' {
			end++
		}
		head := content[:b]
		if strings.HasPrefix(content[b:], hermesBlockBegin+hermesNoEOLFlag) {
			head = strings.TrimSuffix(head, "\n")
		}
		out := head + content[end:]
		if out == "" {
			return os.Remove(path)
		}
		return writeAtomic(path, []byte(out), fileMode(path))
	}
	lines := strings.Split(content, "\n")
	changed := false
	kept := lines[:0:0]
	for _, l := range lines {
		body := strings.TrimSuffix(l, "\r")
		cr := l[len(body):]
		switch {
		case strings.Contains(body, "provider: "+HermesProviderName+hermesAddedTrailer) && strings.HasSuffix(body, hermesAddedTrailer):
			changed = true
			continue
		case strings.Contains(body, "provider: "+HermesProviderName+hermesOrigTrailer):
			i := strings.Index(body, hermesOrigTrailer)
			orig, err := strconv.Unquote(body[i+len(hermesOrigTrailer):])
			if err != nil {
				return fmt.Errorf("%s: unreadable backstory:orig marker: %w", path, err)
			}
			changed = true
			l = orig + cr
		}
		kept = append(kept, l)
	}
	if !changed {
		return nil
	}
	return writeAtomic(path, []byte(strings.Join(kept, "\n")), fileMode(path))
}

func hermesConfigStatus(path string) ItemStatus {
	content, err := readOptional(path)
	if err != nil {
		return StatusAbsent
	}
	switch scanHermesConfig(content).state {
	case provOurs:
		return StatusPresent
	case provForeign:
		return StatusForeign
	}
	return StatusAbsent
}
