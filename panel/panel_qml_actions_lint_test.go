package panel

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// qmlTestsDir holds the QtTest suites; qmlActionsHelper is the one file
// allowed to touch the raw pointer API (task bf001e64).
const (
	qmlTestsDir      = "qmltest/tests"
	qmlActionsHelper = "TestActions.qml"
)

// rawQMLAction matches a raw pointer call (or a direct handler invocation)
// that bypasses the shared settle/click/launch helper. A raw mouseClick +
// wait(20) + immediate assertion is the flake class this lint keeps out:
// under CPU load the layout, the click delivery and the launch record all
// land later than 20 ms. Callers must use TestActions.clickWhenReady /
// activateWhenReady, which wait for readiness and confirm delivery.
var rawQMLAction = regexp.MustCompile(`\bmouse(Click|Press|Release|DoubleClick|Move|Wheel|Drag)\s*\(|\.clicked\s*\(\s*\)`)

// findRawQMLActions returns "file:line: text" for every line of src that
// calls a raw pointer API.
func findRawQMLActions(name, src string) []string {
	return findQMLCode(name, src, rawQMLAction)
}

// findQMLCode returns "file:line: text" for every line of src whose code
// matches re. Comment-only lines are skipped so prose may name what re
// matches; the trailing // comment after code is dropped before matching.
func findQMLCode(name, src string, re *regexp.Regexp) []string {
	var hits []string
	for i, line := range strings.Split(src, "\n") {
		code := line
		if trim := strings.TrimSpace(line); strings.HasPrefix(trim, "//") || strings.HasPrefix(trim, "*") || strings.HasPrefix(trim, "/*") {
			continue
		}
		if cut := strings.Index(code, "//"); cut >= 0 && !strings.Contains(code[:cut], "\"") {
			code = code[:cut]
		}
		if re.MatchString(code) {
			hits = append(hits, name+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
		}
	}
	return hits
}

func qmlTestFiles(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(qmlTestsDir, "*.qml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("no QML tests found under %s", qmlTestsDir)
	}
	return files
}

// TestQMLTestsUseSharedActionHelper fails if any panel QML test calls
// mouseClick(/mousePress( (or another raw pointer call) directly, so a
// click can only be made through TestActions.
func TestQMLTestsUseSharedActionHelper(t *testing.T) {
	for _, path := range qmlTestFiles(t) {
		if filepath.Base(path) == qmlActionsHelper {
			continue
		}
		raw, err := os.ReadFile(path) //nolint:gosec // path is this test's own filepath.Glob("*.qml") result, never external input
		if err != nil {
			t.Fatal(err)
		}
		for _, hit := range findRawQMLActions(filepath.Base(path), string(raw)) {
			t.Errorf("raw pointer action outside %s (use act.clickWhenReady): %s", qmlActionsHelper, hit)
		}
	}
}

// TestQMLSuitesInstantiateSharedActionHelper fails if a tst_*.qml suite
// does not declare a TestActions instance: "every test uses the helper".
func TestQMLSuitesInstantiateSharedActionHelper(t *testing.T) {
	decl := regexp.MustCompile(`\bTestActions\s*\{`)
	for _, path := range qmlTestFiles(t) {
		base := filepath.Base(path)
		if !strings.HasPrefix(base, "tst_") || base == "tst_load_all.qml" {
			continue
		}
		raw, err := os.ReadFile(path) //nolint:gosec // path is this test's own filepath.Glob("*.qml") result, never external input
		if err != nil {
			t.Fatal(err)
		}
		if !decl.Match(raw) {
			t.Errorf("%s does not declare a TestActions { testCase: ... } helper", base)
		}
	}
}

// TestRawQMLActionDetector pins the lint's own matcher so it cannot rot into
// a check that passes vacuously.
func TestRawQMLActionDetector(t *testing.T) {
	bad := []string{
		"    mouseClick(button, 1, 2)",
		"mouseClick (button)",
		"x.mousePress(item)",
		"  mouseRelease(item)",
		"  mouseDoubleClick(item)",
		"  mouseWheel(item, 0, 0, 0, 120)",
		"    button.clicked()",
		"    var a = 1; mouseClick(x) // trailing comment",
	}
	for _, line := range bad {
		if len(findRawQMLActions("f.qml", line)) == 0 {
			t.Errorf("detector missed %q", line)
		}
	}
	good := []string{
		"    act.clickWhenReady(button)",
		"    // mouseClick() is what the helper wraps",
		"    * mouseClick(",
		"    var s = x.testClickCount",
	}
	for _, line := range good {
		if hits := findRawQMLActions("f.qml", line); len(hits) != 0 {
			t.Errorf("detector flagged %q: %v", line, hits)
		}
	}
}
