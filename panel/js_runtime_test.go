package panel

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// pragmaLinePattern strips QML's `.pragma library` directive, which is
// QML-JS-only syntax that Node's plain JS engine rejects; every plugin
// library file under js/ starts with it (see e.g. js/launchers.js).
var pragmaLinePattern = regexp.MustCompile(`(?m)^\s*\.pragma\s+library\s*$`)

// requireNode skips the calling test when Node isn't on PATH, so `make
// check` still exits 0 on a machine without Node (the plugin itself needs
// only Quickshell/QML at runtime, never Node — see panel/README.md). Here,
// in this environment, node is present and these tests run for real.
func requireNode(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH; skipping JS-execution proof (see panel/README.md Testing)")
	}
	return path
}

// evalJS loads the named library files (each with its `.pragma library`
// directive stripped) and evaluates expr against them under Node,
// returning expr's JSON-encoded result. This runs the plugin's actual .js
// files byte-for-byte, not a Go reimplementation of their logic, so a test
// built on it exercises exactly what the QML engine would execute.
func evalJS(t *testing.T, expr string, libPaths ...string) json.RawMessage {
	t.Helper()
	sources := make([]string, 0, len(libPaths))
	for _, p := range libPaths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		sources = append(sources, string(raw))
	}
	return evalJSSource(t, expr, sources...)
}

// evalJSSource is evalJS's lower layer: it takes library source text
// directly instead of file paths, so a test can evaluate a deliberately
// mutated copy of a library (the critic-mutation proof pattern) without
// writing it over the real file on disk.
func evalJSSource(t *testing.T, expr string, sources ...string) json.RawMessage {
	t.Helper()
	nodeBin := requireNode(t)

	var src strings.Builder
	for _, s := range sources {
		src.WriteString(pragmaLinePattern.ReplaceAllString(s, ""))
		src.WriteString("\n")
	}
	src.WriteString("process.stdout.write(JSON.stringify(" + expr + "))\n")

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "eval.js")
	if err := os.WriteFile(scriptPath, []byte(src.String()), 0o600); err != nil {
		t.Fatalf("write temp script: %v", err)
	}

	out, err := exec.Command(nodeBin, scriptPath).CombinedOutput()
	if err != nil {
		t.Fatalf("node %s: %v\n%s", scriptPath, err, out)
	}
	return json.RawMessage(out)
}

// jsStringLiteral renders s as a JS string literal (via JSON encoding,
// which is also valid JS string-literal syntax), so a Go string with
// quotes or other special characters can be substituted into an `expr`
// passed to evalJS without hand-rolled escaping.
func jsStringLiteral(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}
