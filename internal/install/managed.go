package install

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"regexp"
	"slices"
	"strings"
)

// StatusEdited marks a whole file Backstory wrote that no longer matches any
// version Backstory shipped: the owner changed it. Install and --remove leave
// it exactly as it is.
const StatusEdited ItemStatus = "edited"

// managedFile is a file whose whole content Backstory writes from an embedded
// source (the Pi extension, the Hermes plugin files, the sync units). Install
// replaces it only while it holds a version Backstory shipped; anything else
// is the owner's edit and is kept.
type managedFile struct {
	// source is the embedded file, relative to this package's directory.
	source string
	// shipped is the sha256 (hex) of every version of source committed to
	// this repo, the current one included (managed_test.go checks it against
	// git history and the working tree).
	shipped []string
	// owned reports whether content carries Backstory's marker; nil when the
	// file has none. Content without it is someone else's (StatusForeign).
	owned func(content string) bool
	// unrender maps an installed file back to source's form, undoing what
	// install fills in (the binary path, the tool schemas); nil when the file
	// is written verbatim. What it undoes is Backstory's, so an edit there is
	// not seen.
	unrender func(content string) string
}

// status reports the file at path against want, the bytes install would
// write now: absent, present (want), foreign (no marker), outdated (a
// version Backstory shipped) or edited (anything else).
func (m managedFile) status(path, want string) (ItemStatus, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a Backstory-managed path under the caller's home
	if os.IsNotExist(err) {
		return StatusAbsent, nil
	}
	if err != nil {
		return "", err
	}
	content := string(data)
	switch {
	case content == want:
		return StatusPresent, nil
	case m.owned != nil && !m.owned(content):
		return StatusForeign, nil
	case m.isShipped(content):
		return StatusOutdated, nil
	default:
		return StatusEdited, nil
	}
}

func (m managedFile) isShipped(content string) bool {
	if m.unrender != nil {
		content = m.unrender(content)
	}
	sum := sha256.Sum256([]byte(content))
	return slices.Contains(m.shipped, hex.EncodeToString(sum[:]))
}

// managedFiles is every managedFile, the population managed_test.go holds to
// the package's //go:embed directives.
var managedFiles = []managedFile{piExtensionFile, hermesPluginYAMLFile, hermesPluginPyFile, syncServiceFile, syncTimerFile}

var piExtensionFile = managedFile{
	source: "piext/index.ts",
	shipped: []string{
		"96ccb3798d31b676c44ae890a44dc2801b1f507ff73e40e6e42fb484aa05bd83", // ae2cc57
		"0cb75536f067912fe459c12c0bbe3a9759af1a35b2bac61f631ad9b0fc57191a", // 096a2dc
	},
	owned: func(c string) bool { return strings.Contains(c, PiExtensionMarker) },
}

var hermesPluginYAMLFile = managedFile{
	source: "hermes_plugin/plugin.yaml",
	shipped: []string{
		"ee7941711869b85db96f78b8d1ae9ac51ee663c32bc52357f7f4a225458a2e83", // a6096c5
	},
	owned: isOurs,
}

var hermesPluginPyFile = managedFile{
	source: "hermes_plugin/__init__.py",
	shipped: []string{
		"98b4e19c19742d44a387e1627a4e6acf60373b9670afa6159a8d8ea69f92e687", // a6096c5
		"34795ef81b9147dfa74db14b7cac511a5f673d25c64e669871fcbeb41e29d01d", // 9e0fa25
		"da75dec43948dc66c39f0fd55696373763904b706768184ca14356de069fc992", // 0c94e43
		"1c28f242d130de449a3f757c376e2e56ce9714e4ba27798969081e71336b05bd", // cdae5c7, 1877ea0
		"24549c93c46d67d27024cea557f78f84d3de9564e4725b52082069478d4b8010", // 840711d, 3ebdb84
	},
	owned:    isOurs,
	unrender: unrenderHermesTools,
}

var syncServiceFile = managedFile{
	source: "units/backstory-sync.service",
	shipped: []string{
		"4c452a7367d8f77045677b4739bbb86b98cf7c598d2a093df83e5e99bedaf814", // e6951ea
	},
	unrender: unrenderSyncExecStart,
}

var syncTimerFile = managedFile{
	source: "units/backstory-sync.timer",
	shipped: []string{
		"0eb0bf68aac09944c9011704b1c73a5e5231e3bfe45a61b01e4c6002662ddd26", // e6951ea
	},
}

// hermesToolsOpen opens the literal HermesPluginFiles fills with the tool
// schemas; the JSON never holds a triple quote, so the next one closes it.
const hermesToolsOpen = "TOOL_SCHEMAS = json.loads(r'''"

// unrenderHermesTools puts hermesToolsToken back where the tool schemas were.
func unrenderHermesTools(c string) string {
	i := strings.Index(c, hermesToolsOpen)
	if i < 0 {
		return c
	}
	start := i + len(hermesToolsOpen)
	end := strings.Index(c[start:], "'''")
	if end < 0 {
		return c
	}
	return c[:start] + hermesToolsToken + c[start+end:]
}

// syncExecStartRE is the ExecStart binary SyncServiceUnitFor writes: a bare
// word, or a double-quoted path (systemdExecPath).
var syncExecStartRE = regexp.MustCompile(`(?m)^ExecStart=("(?:[^"\\]|\\.)*"|[^\s"]+) `)

// unrenderSyncExecStart puts SyncUnitTemplateBinary back as the ExecStart binary.
func unrenderSyncExecStart(c string) string {
	loc := syncExecStartRE.FindStringSubmatchIndex(c)
	if loc == nil {
		return c
	}
	return c[:loc[2]] + SyncUnitTemplateBinary + c[loc[3]:]
}

// EditedNotice is the line install or --remove prints for a managed file it
// kept because the owner edited it, or because it is a symlink the owner
// put there.
func EditedNotice(harness, path string, removing bool) string {
	if isSymlink(path) {
		if removing {
			return harness + ": kept your symlinked " + path + "; delete it yourself if you no longer want it"
		}
		return harness + ": kept your symlinked " + path + ", so it was not replaced; update the file it links to, or replace the link, to get the current version"
	}
	if removing {
		return harness + ": kept " + path + ": it was edited after Backstory wrote it; delete it yourself if you no longer want it"
	}
	return harness + ": kept " + path + ": it was edited after Backstory wrote it, so it was not replaced; move it aside and install again to get the current version"
}

// OwnerHookNotice is the line install prints for a hook event it left as
// the owner's: the event already runs Backstory's hook through a command the
// owner wrote (ownerWrapsHook), so install added none of its own there.
func OwnerHookNotice(harness, event, path string) string {
	return harness + ": kept your " + event + " hook in " + path + ": it runs Backstory through a command you wrote, so Backstory added none of its own; remove yours and install again to get Backstory's"
}

// ownerHookNotices is an OwnerHookNotice for every hook item of a successful
// install that --check reports foreign-conflict; events maps those item
// names to their event, in report order.
func ownerHookNotices(harness, path string, items []Item, events map[string]string) []string {
	var lines []string
	for _, it := range items {
		if ev, ok := events[it.Name]; ok && it.Status == StatusForeign {
			lines = append(lines, OwnerHookNotice(harness, ev, path))
		}
	}
	return lines
}

// ShippedSHA256 is the shipped list of the managed file embedded from source
// (relative to this package, e.g. "units/backstory-sync.timer"); nil when no
// managed file has that source. ops/install.sh carries the sync units' lists
// too, and a test holds the two equal.
func ShippedSHA256(source string) []string {
	for _, m := range managedFiles {
		if m.source == source {
			return slices.Clone(m.shipped)
		}
	}
	return nil
}
