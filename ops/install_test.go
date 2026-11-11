package ops

import (
	"bytes"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// installFixture is an empty HOME plus recording fakes for systemctl, pgrep
// (the session-lock probe) and omarchy-restart-shell, put first on PATH so
// the test never touches the runner's real systemd or shell. The rest of
// PATH is hostTools: the runner's tools minus its omarchy CLI.
type installFixture struct {
	t      *testing.T
	home   string
	cfg    string
	fakes  string
	host   string
	log    string
	binOut string
	run    string   // XDG_RUNTIME_DIR: the daemon socket lives under it
	extra  []string // per-test env, appended last
}

func newInstallFixture(t *testing.T) *installFixture {
	t.Helper()
	root := t.TempDir()
	f := &installFixture{
		t:      t,
		home:   filepath.Join(root, "home"),
		fakes:  filepath.Join(root, "fakes"),
		host:   filepath.Join(root, "host"),
		log:    filepath.Join(root, "calls.log"),
		binOut: filepath.Join(root, "build", "backstory"),
		run:    filepath.Join(root, "run"),
	}
	f.cfg = filepath.Join(f.home, ".config")
	for _, d := range []string{f.home, f.fakes, f.host} {
		if err := os.MkdirAll(d, 0o755); err != nil { //nolint:gosec // fixture path under t.TempDir
			t.Fatal(err)
		}
	}
	hostTools(t, f.host)
	// enable --now / restart "start the unit": mark it active and run the
	// installed daemon detached (unless FAKE_NODAEMON), as systemd would.
	f.fake("systemctl", `echo "systemctl $*" >> "$FAKE_LOG"
pids="$FAKE_LOG.daemon-pids"
stopdaemon() { [ -f "$pids" ] && while read -r p; do kill "$p" 2>/dev/null; n=0; while kill -0 "$p" 2>/dev/null && [ "$n" -lt 100 ]; do sleep 0.1; n=$((n+1)); done; done < "$pids"; : > "$pids"; }
case "$*" in
*"stop backstory-shell-restart"*) [ -f "$FAKE_LOG.unit-pid" ] && kill "$(cat "$FAKE_LOG.unit-pid")" 2>/dev/null; rm -f "$FAKE_LOG.unit-pid" ;;
*is-active*) [ -e "$FAKE_ACTIVE" ]; exit ;;
*"enable --now"*|*restart*)
	: > "$FAKE_ACTIVE"
	stopdaemon
	if [ -z "$FAKE_NODAEMON" ]; then
		setsid "$HOME/.local/bin/backstory" daemon >"$FAKE_LOG.daemon-out" 2>&1 </dev/null &
		echo $! >> "$pids"
	fi ;;
*stop*) stopdaemon ;;
esac
exit 0`)
	t.Cleanup(func() {
		b, _ := os.ReadFile(f.log + ".daemon-pids")
		for _, p := range strings.Fields(string(b)) {
			if n, err := strconv.Atoi(p); err == nil {
				_ = syscall.Kill(n, syscall.SIGTERM)
			}
		}
	})
	f.fake("pgrep", `echo "pgrep $*" >> "$FAKE_LOG"; [ -e "$FAKE_LOCKED" ]`)
	f.fake("omarchy-restart-shell", `echo "omarchy-restart-shell" >> "$FAKE_LOG"`)
	// hyprctl: record the call; reload fails when FAKE_HYPR_RELOAD_FAILS
	// exists; configerrors prints FAKE_HYPR_ERRORS when it exists, and fails
	// when FAKE_HYPR_CHECK_FAILS does. The runner's own hyprctl would reach
	// its live Hyprland.
	f.fake("hyprctl", `echo "hyprctl $*" >> "$FAKE_LOG"
[ "$1" = reload ] && [ -e "$FAKE_HYPR_RELOAD_FAILS" ] && exit 3
[ "$1" = configerrors ] && [ -e "$FAKE_HYPR_CHECK_FAILS" ] && exit 3
[ "$1" = configerrors ] && [ -f "$FAKE_HYPR_ERRORS" ] && cat "$FAKE_HYPR_ERRORS"
exit 0`)
	// systemd-run: record the call; the unit does not run unless a test
	// replaces this stub (see runDeferredUnit).
	f.fake("systemd-run", `echo "systemd-run $1 $2 $3 $4" >> "$FAKE_LOG"`)
	return f
}

// runDeferredUnit makes the systemd-run fake behave like a transient unit:
// it runs the command detached (as the pending unit) and remembers its pid so
// `systemctl stop` of the unit kills it.
func (f *installFixture) runDeferredUnit() {
	f.fake("systemd-run", `echo "systemd-run $1 $2 $3 $4" >> "$FAKE_LOG"
while [ $# -gt 0 ]; do case "$1" in --*) shift ;; *) break ;; esac; done
setsid "$@" >/dev/null 2>&1 </dev/null &
echo $! > "$FAKE_LOG.unit-pid"`)
}

func (f *installFixture) fake(name, body string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.fakes, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil { //nolint:gosec // fixture path under t.TempDir
		f.t.Fatal(err)
	}
}

func (f *installFixture) flag(name string, on bool) {
	f.t.Helper()
	p := filepath.Join(filepath.Dir(f.log), name)
	if on {
		if err := os.WriteFile(p, nil, 0o644); err != nil { //nolint:gosec // fixture path under t.TempDir
			f.t.Fatal(err)
		}
	} else {
		_ = os.Remove(p)
	}
}

func goEnv(t *testing.T, key string) string {
	t.Helper()
	cmd := exec.Command("go", "env", key) //nolint:gosec // fixed args
	// the go tool writes telemetry under XDG_CONFIG_HOME; keep that out of any dir a test watches
	cmd.Env = append(os.Environ(), "XDG_CONFIG_HOME="+t.TempDir())
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// env returns os.Environ() with HOME and every XDG base dir (CONFIG, DATA,
// STATE, CACHE, RUNTIME) dropped and re-pointed under the fixture, then
// overrides appended. Every test subprocess that runs the installer or the
// installed binary builds its env through this, so no inherited XDG_* value
// can send a write (usage file, config, cache) to the developer's real dirs.
func (f *installFixture) env(overrides ...string) []string {
	owned := map[string]bool{"HOME": true, "XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true, "XDG_STATE_HOME": true, "XDG_CACHE_HOME": true, "XDG_RUNTIME_DIR": true}
	var out []string
	for _, e := range os.Environ() {
		if k, _, _ := strings.Cut(e, "="); !owned[k] {
			out = append(out, e)
		}
	}
	out = append(out,
		"HOME="+f.home,
		"XDG_CONFIG_HOME="+f.cfg,
		"XDG_DATA_HOME="+filepath.Join(f.home, ".local", "share"),
		"XDG_STATE_HOME="+filepath.Join(f.home, ".local", "state"),
		"XDG_CACHE_HOME="+filepath.Join(f.home, ".cache"),
		"XDG_RUNTIME_DIR="+f.run,
	)
	return append(out, overrides...)
}

// status runs the installed `backstory status` (read-only) from dir.
func (f *installFixture) status(dir string) ([]byte, error) {
	ctl := exec.Command(f.path(".local", "bin", "backstory"), "status") //nolint:gosec // fixture path under t.TempDir
	ctl.Dir = dir
	ctl.Env = f.env()
	return ctl.CombinedOutput()
}

// fixtureHealthTimeout is the daemon health wait (seconds) for fixture installs.
// It returns as soon as the daemon answers, so a generous value costs nothing
// when healthy and keeps the test from flaking when the whole suite loads the
// machine (the daemon start once exceeded 10s under `make check`).
const fixtureHealthTimeout = "60"

// make runs `make <target>` in the repo against the fixture and returns the
// combined output and the exit error.
func (f *installFixture) make(target string) (string, error) {
	f.t.Helper()
	cmd := f.makeCmd(target)
	out, err := cmd.CombinedOutput()
	if err != nil && strings.Contains(string(out), "not healthy") {
		return string(out) + f.healthDiagnostics(), err
	}
	return string(out), err
}

// makeCmd builds (without starting) the `make <target>` command make runs.
func (f *installFixture) makeCmd(target string) *exec.Cmd {
	f.t.Helper()
	cmd := exec.Command("make", "-C", "..", target, "BIN="+f.binOut) //nolint:gosec // fixture path under t.TempDir
	cmd.Env = f.env(
		"BACKSTORY_HEALTH_TIMEOUT="+fixtureHealthTimeout,
		"PATH="+f.fakes+string(os.PathListSeparator)+f.host,
		"FAKE_LOG="+f.log,
		"FAKE_ACTIVE="+filepath.Join(filepath.Dir(f.log), "active"),
		"FAKE_LOCKED="+filepath.Join(filepath.Dir(f.log), "locked"),
		"FAKE_HYPR_ERRORS="+filepath.Join(filepath.Dir(f.log), "hypr-errors"),
		"FAKE_HYPR_CHECK_FAILS="+filepath.Join(filepath.Dir(f.log), "hypr-check-fails"),
		"FAKE_HYPR_RELOAD_FAILS="+filepath.Join(filepath.Dir(f.log), "hypr-reload-fails"),
		"HYPRLAND_INSTANCE_SIGNATURE=", // an ssh session has none; never inherit the developer's
		// keep the build off the (empty) fixture HOME
		"GOCACHE="+goEnv(f.t, "GOCACHE"),
		"GOMODCACHE="+goEnv(f.t, "GOMODCACHE"),
	)
	cmd.Env = append(cmd.Env, f.extra...)
	return cmd
}

// healthDiagnostics is what a failed health wait needs to explain itself: the
// fixture daemon's own output and the fake command log, both of which die with
// the test's TempDir.
func (f *installFixture) healthDiagnostics() string {
	section := func(title, path string) string {
		b, err := os.ReadFile(path) //nolint:gosec // fixture path under t.TempDir
		body := string(b)
		if err != nil {
			body = "(unreadable: " + err.Error() + ")\n"
		} else if body == "" {
			body = "(empty)\n"
		}
		return "\n--- " + title + " (" + path + ") ---\n" + body
	}
	return section("fixture daemon output", f.log+".daemon-out") + section("fake command log", f.log)
}

func (f *installFixture) calls() string {
	b, _ := os.ReadFile(f.log)
	return string(b)
}

func (f *installFixture) resetCalls() { _ = os.Remove(f.log) }

func (f *installFixture) path(rel ...string) string {
	return filepath.Join(append([]string{f.home}, rel...)...)
}

func (f *installFixture) mustMake(target string) string {
	f.t.Helper()
	out, err := f.make(target)
	if err != nil {
		f.t.Fatalf("make %s: %v\n%s", target, err, out)
	}
	return out
}

func (f *installFixture) panelDir() string {
	return filepath.Join(f.cfg, "omarchy", "plugins", "ridgetopai.backstory")
}

func (f *installFixture) panelBackup() string {
	return filepath.Join(f.cfg, "omarchy", "plugins", ".ridgetopai.backstory.prev")
}

// treeMap reads every file under dir into a relpath -> content map.
func treeMap(t *testing.T, dir string) map[string]string {
	t.Helper()
	m := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() {
			m[rel+"/"] = ""
			return nil
		}
		b, err := os.ReadFile(p) //nolint:gosec // fixture path under t.TempDir
		if err != nil {
			return err
		}
		m[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func mustExist(t *testing.T, p string) {
	t.Helper()
	if _, err := os.Lstat(p); err != nil {
		t.Errorf("expected %s to exist: %v", p, err)
	}
}

func mustNotExist(t *testing.T, p string) {
	t.Helper()
	if _, err := os.Lstat(p); err == nil {
		t.Errorf("expected %s to be gone", p)
	}
}

func TestMakeInstallFirstInstall(t *testing.T) {
	f := newInstallFixture(t)
	out := f.mustMake("install")
	t.Log(out)

	if fi, err := os.Stat(f.path(".local", "bin", "backstory")); err != nil || fi.Mode()&0o111 == 0 {
		t.Errorf("binary not installed executable: %v", err)
	}
	unit, err := os.ReadFile(f.path(".config", "systemd", "user", "backstory.service"))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile("backstory.service")
	if !bytes.Equal(unit, want) {
		t.Error("installed unit differs from ops/backstory.service")
	}
	assertPanelRuntimeOnly(t, f.panelDir())
	hypr, err := os.ReadFile(f.path(".config", "hypr", "backstory.lua"))
	if err != nil {
		t.Fatal(err)
	}
	wantHypr, _ := os.ReadFile("hyprland/backstory.lua")
	if !bytes.Equal(hypr, wantHypr) {
		t.Error("installed hypr file differs from ops/hyprland/backstory.lua")
	}
	mustNotExist(t, f.path(".local", "bin", "backstory.prev"))

	calls := f.calls()
	for _, w := range []string{"systemctl --user daemon-reload", "systemctl --user enable --now backstory"} {
		if !strings.Contains(calls, w) {
			t.Errorf("service-manager log lacks %q:\n%s", w, calls)
		}
	}
	if strings.Contains(calls, "restart backstory") {
		t.Errorf("first install must not restart:\n%s", calls)
	}
	if !strings.Contains(out, `require("hypr.backstory")`) {
		t.Errorf("output lacks the hyprland.lua line to add:\n%s", out)
	}
}

// writeHyprland puts a hyprland.lua with the given content in the fixture.
func (f *installFixture) writeHyprland(content string) (path string, orig []byte) {
	f.t.Helper()
	path = f.path(".config", "hypr", "hyprland.lua")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // fixture path under t.TempDir
		f.t.Fatal(err)
	}
	orig = []byte(content)
	if err := os.WriteFile(path, orig, 0o644); err != nil { //nolint:gosec // fixture path under t.TempDir
		f.t.Fatal(err)
	}
	return path, orig
}

const addLineMessage = "add this line to your hyprland.lua"

func TestInstallerSaysKeybindAlreadyLoaded(t *testing.T) {
	f := newInstallFixture(t)
	hl, orig := f.writeHyprland("-- my config\nrequire(\"hypr.other\")\nrequire(\"hypr.backstory\")\n")
	out := f.mustMake("install")
	t.Log(out)
	if !strings.Contains(out, "keybind already loaded from hyprland.lua") {
		t.Errorf("output does not say the keybind is already loaded:\n%s", out)
	}
	if strings.Contains(out, addLineMessage) {
		t.Errorf("output tells the user to add a line that is already there:\n%s", out)
	}
	if got, _ := os.ReadFile(hl); !bytes.Equal(got, orig) { //nolint:gosec // fixture path under t.TempDir
		t.Errorf("hyprland.lua was modified:\n%s", got)
	}
}

// With hyprland.lua loading backstory.lua and a Hyprland to reach, install
// reloads Hyprland and checks its config, as Omarchy's own changes do.
func TestInstallerReloadsHyprlandWhenItLoadsBackstory(t *testing.T) {
	f := newInstallFixture(t)
	f.writeHyprland("require(\"hypr.backstory\")\n")
	f.extra = append(f.extra, "HYPRLAND_INSTANCE_SIGNATURE=fixture")
	out := f.mustMake("install")
	t.Log(out)
	for _, want := range []string{"hyprctl reload", "hyprctl configerrors"} {
		if !strings.Contains(f.calls(), want) {
			t.Errorf("install never ran %q:\n%s", want, f.calls())
		}
	}
	if !strings.Contains(out, "reloaded Hyprland") || strings.Contains(out, "warning:") {
		t.Errorf("install did not report a clean reload:\n%s", out)
	}
}

// A config error after the reload is a warning: the install still succeeds.
func TestInstallerWarnsOnHyprlandConfigErrors(t *testing.T) {
	f := newInstallFixture(t)
	f.writeHyprland("require(\"hypr.backstory\")\n")
	f.extra = append(f.extra, "HYPRLAND_INSTANCE_SIGNATURE=fixture")
	if err := os.WriteFile(filepath.Join(filepath.Dir(f.log), "hypr-errors"), []byte("backstory.lua:3: unexpected symbol\n"), 0o644); err != nil { //nolint:gosec // fixture path under t.TempDir
		t.Fatal(err)
	}
	out := f.mustMake("install")
	t.Log(out)
	if !strings.Contains(out, "warning: Hyprland reports config errors after reloading:") || !strings.Contains(out, "backstory.lua:3: unexpected symbol") {
		t.Errorf("install did not warn with Hyprland's config errors:\n%s", out)
	}
}

// A config check that fails is a warning too: the install goes on to the
// panel and the rest (found by review: under set -e it stopped there).
func TestInstallerGoesOnWhenTheConfigCheckFails(t *testing.T) {
	f := newInstallFixture(t)
	f.writeHyprland("require(\"hypr.backstory\")\n")
	f.extra = append(f.extra, "HYPRLAND_INSTANCE_SIGNATURE=fixture")
	f.flag("hypr-check-fails", true)
	out := f.mustMake("install")
	t.Log(out)
	if !strings.Contains(out, "warning: reloaded Hyprland but could not check its config") {
		t.Errorf("install did not warn that the check failed:\n%s", out)
	}
	mustExist(t, filepath.Join(f.panelDir(), "Panel.qml"))
}

// A reload that fails is a warning: the install goes on to the panel and
// the rest, and skips the config check, which would read the old config.
func TestInstallerGoesOnWhenTheReloadFails(t *testing.T) {
	f := newInstallFixture(t)
	f.writeHyprland("require(\"hypr.backstory\")\n")
	f.extra = append(f.extra, "HYPRLAND_INSTANCE_SIGNATURE=fixture")
	f.flag("hypr-reload-fails", true)
	out := f.mustMake("install")
	t.Log(out)
	if !strings.Contains(out, "warning: could not reload Hyprland; the keybind applies at its next reload") {
		t.Errorf("install did not warn that the reload failed:\n%s", out)
	}
	if strings.Contains(f.calls(), "hyprctl configerrors") {
		t.Errorf("install checked the config after a failed reload:\n%s", f.calls())
	}
	mustExist(t, filepath.Join(f.panelDir(), "Panel.qml"))
}

// Without the require line, or with no Hyprland to reach, install leaves
// Hyprland alone.
func TestInstallerLeavesHyprlandUnreloadedWhenItCannotApply(t *testing.T) {
	for name, tc := range map[string]struct{ lua, sig string }{
		"not loaded":  {"require(\"hypr.other\")\n", "fixture"},
		"no hyprland": {"require(\"hypr.backstory\")\n", ""},
	} {
		t.Run(name, func(t *testing.T) {
			f := newInstallFixture(t)
			f.writeHyprland(tc.lua)
			f.extra = append(f.extra, "HYPRLAND_INSTANCE_SIGNATURE="+tc.sig)
			out := f.mustMake("install")
			if strings.Contains(f.calls(), "hyprctl") {
				t.Errorf("install ran hyprctl:\n%s\n%s", f.calls(), out)
			}
		})
	}
}

func TestInstallerPrintsAddLineWhenMissingOrCommentedOut(t *testing.T) {
	for name, content := range map[string]string{
		"absent":        "-- my config\nrequire(\"hypr.other\")\n",
		"commented out": "-- require(\"hypr.backstory\")\n",
	} {
		t.Run(name, func(t *testing.T) {
			f := newInstallFixture(t)
			hl, orig := f.writeHyprland(content)
			out := f.mustMake("install")
			t.Log(out)
			if !strings.Contains(out, addLineMessage) || !strings.Contains(out, `    require("hypr.backstory")`) {
				t.Errorf("add-this-line message missing:\n%s", out)
			}
			if strings.Contains(out, "already loaded") {
				t.Errorf("claims the keybind is loaded when it is not:\n%s", out)
			}
			if got, _ := os.ReadFile(hl); !bytes.Equal(got, orig) { //nolint:gosec // fixture path under t.TempDir
				t.Errorf("hyprland.lua was modified:\n%s", got)
			}
		})
	}
}

func TestInstallerReportsVersionAndNext(t *testing.T) {
	f := newInstallFixture(t)
	out := f.mustMake("install")
	t.Log(out)
	m := regexp.MustCompile(`(?m)^backstory (\S+) installed$`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no \"backstory <version> installed\" line:\n%s", out)
	}
	want, err := exec.Command(f.path(".local", "bin", "backstory"), "version").Output() //nolint:gosec // fixture path under t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	if m[1] != strings.TrimSpace(string(want)) {
		t.Errorf("labelled version %q, binary says %q", m[1], want)
	}
	if !regexp.MustCompile(`(?m)^next: .*CTRL\+SHIFT\+B.*next session`).MatchString(out) {
		t.Errorf("no next: line naming the keybind and next session:\n%s", out)
	}
	var last string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if !strings.HasPrefix(l, "make:") && !strings.HasPrefix(l, "make[") { // make's own directory banner
			last = l
		}
	}
	if !strings.HasPrefix(last, "next: ") || strings.Count(out, "\nnext: ") != 1 {
		t.Errorf("want exactly one next: line, last; last line is %q", last)
	}
}

// A daemon that accepts connections and never answers: the installer must
// give up within its named timeout, name the daemon, and exit non-zero.
func TestInstallerFailsWhenDaemonNeverAnswers(t *testing.T) {
	f := newInstallFixture(t)
	if err := os.MkdirAll(filepath.Join(f.run, "backstory"), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", filepath.Join(f.run, "backstory", "sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer func() { _ = c.Close() }() // held open, never answered
		}
	}()
	f.extra = []string{"FAKE_NODAEMON=1", "BACKSTORY_HEALTH_TIMEOUT=2", "BACKSTORY_HEALTH_ATTEMPT_TIMEOUT=1"}
	start := time.Now()
	out, err := f.make("install")
	if err == nil {
		t.Fatalf("install succeeded against a daemon that never answers:\n%s", out)
	}
	if !strings.Contains(out, "backstory.service") || !strings.Contains(out, "not healthy") {
		t.Errorf("failure line does not name the daemon:\n%s", out)
	}
	if strings.Contains(out, "installed\n") && regexp.MustCompile(`(?m)^backstory \S+ installed$`).MatchString(out) {
		t.Errorf("reported success:\n%s", out)
	}
	if d := time.Since(start); d > 90*time.Second {
		t.Errorf("health check not bounded: took %s", d)
	}
}

func TestInstallerFailsWhenNoDaemonRuns(t *testing.T) {
	f := newInstallFixture(t)
	f.extra = []string{"FAKE_NODAEMON=1", "BACKSTORY_HEALTH_TIMEOUT=1"}
	out, err := f.make("install")
	if err == nil || !strings.Contains(out, "backstory.service") || !strings.Contains(out, "not healthy") {
		t.Fatalf("want a named daemon failure, got err=%v:\n%s", err, out)
	}
}

// A failed health wait must carry the fixture daemon's output and the fake
// command log: both live in the TempDir, so this is the only chance to see them.
func TestFixtureHealthFailureShowsDaemonOutputAndFakeLog(t *testing.T) {
	f := newInstallFixture(t)
	f.extra = []string{"FAKE_NODAEMON=1", "BACKSTORY_HEALTH_TIMEOUT=1"}
	out, err := f.make("install")
	if err == nil {
		t.Fatalf("install succeeded with no daemon:\n%s", out)
	}
	for _, want := range []string{"--- fixture daemon output", "--- fake command log", "systemctl --user enable --now"} {
		if !strings.Contains(out, want) {
			t.Errorf("failure text lacks %q:\n%s", want, out)
		}
	}
}

// The installer runs from the clone dir. Its own backstory calls must leave
// no project row and no session for that dir in the user's store; a plain
// `backstory status` from the same dir (the control) does record one.
func TestInstallerRecordsNoProjectForBuildDir(t *testing.T) {
	f := newInstallFixture(t)
	// A Claude harness is present (the normal tester case), so `backstory
	// install` runs its hook verification against the daemon.
	if err := os.MkdirAll(f.path(".claude"), 0o755); err != nil { //nolint:gosec // fixture path under t.TempDir
		t.Fatal(err)
	}
	// ~/.local/bin is on a real tester's PATH, so the hook command
	// (`backstory hook session-start`) resolves to the installed binary and
	// really dials the daemon.
	f.extra = append(f.extra, "PATH="+f.fakes+string(os.PathListSeparator)+f.path(".local", "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	out := f.mustMake("install")
	if strings.Contains(out, "hook verification failed") || !strings.Contains(out, "claude") {
		t.Fatalf("hook verification did not run and pass:\n%s", out)
	}
	clone, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dbPath := f.path(".local", "share", "backstory", "backstory.db")

	count := func(query string, args ...any) int {
		t.Helper()
		st, err := store.Open(dbPath, nil, project.RealGit{})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = st.Close() }()
		var n int
		if err := st.DB().QueryRow(query, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	sessionsAt := func() int { return count(`SELECT count(*) FROM sessions WHERE cwd = ?`, clone) }
	projects := func() int { return count(`SELECT count(*) FROM projects`) }

	if n := sessionsAt(); n != 0 {
		t.Errorf("installer minted %d session(s) with cwd %s", n, clone)
	}
	if n := projects(); n != 0 {
		t.Errorf("installer recorded %d project row(s) in the user's store", n)
	}

	// A plain `backstory status` from the clone dir is a read-only call and
	// mints no session either.
	if out, err := f.status(clone); err != nil {
		t.Fatalf("control status: %v\n%s", err, out)
	}
	if n := sessionsAt(); n != 0 {
		t.Errorf("plain `backstory status` minted %d session(s)", n)
	}
}

func TestInstallerFailsEarlyOnOldGo(t *testing.T) {
	f := newInstallFixture(t)
	f.fake("go", `echo "go $*" >> "$FAKE_LOG"
echo "go version go1.20.4 linux/amd64"`)
	out, err := f.make("install")
	if err == nil {
		t.Fatalf("install succeeded with an old go:\n%s", out)
	}
	need := goDirective(t)
	for _, w := range []string{need, "mise use -g go@" + need} {
		if !strings.Contains(out, w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
	if strings.Contains(f.calls(), "go build") || strings.Contains(f.calls(), "go env") {
		t.Errorf("built before the version check:\n%s", f.calls())
	}
	mustNotExist(t, f.binOut)
	mustNotExist(t, f.path(".local", "bin", "backstory"))
}

func TestInstallerFailsWhenGoMissing(t *testing.T) {
	cmd := exec.Command("sh", "../ops/install.sh", "check-go", "go-that-does-not-exist")
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "mise use -g go@"+goDirective(t)) {
		t.Errorf("want failure naming mise command, got err=%v:\n%s", err, out)
	}
}

// goDirective is the version on go.mod's `go` line.
func goDirective(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^go\s+(\d+(?:\.\d+)*)\s*$`).FindSubmatch(b)
	if m == nil {
		t.Fatal("no go directive in go.mod")
	}
	return string(m[1])
}

func TestReadmeNamesRequiredGoVersion(t *testing.T) {
	need := goDirective(t)
	readme, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{"Go " + need, "`mise use -g go@" + need + "`"} {
		if !strings.Contains(string(readme), w) {
			t.Errorf("README.md lacks %q", w)
		}
	}
}

func TestMakeInstallUpgrade(t *testing.T) {
	f := newInstallFixture(t)
	f.mustMake("install")
	oldBin, _ := os.ReadFile(f.path(".local", "bin", "backstory"))

	// A distinguishable previous panel, still a Backstory panel.
	stale := filepath.Join(f.panelDir(), "stale-from-old-version.qml")
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil { //nolint:gosec // fixture path under t.TempDir
		t.Fatal(err)
	}
	f.flag("active", true)
	f.resetCalls()
	f.mustMake("install")

	calls := f.calls()
	if !strings.Contains(calls, "systemctl --user restart backstory") {
		t.Errorf("upgrade of an active unit did not restart:\n%s", calls)
	}
	if strings.Contains(calls, "enable --now") {
		t.Errorf("active unit must be restarted, not enable --now'd:\n%s", calls)
	}
	prev, err := os.ReadFile(f.path(".local", "bin", "backstory.prev"))
	if err != nil {
		t.Fatalf("previous binary not kept: %v", err)
	}
	if !bytes.Equal(prev, oldBin) {
		t.Error("backstory.prev is not the previously installed binary")
	}
	mustExist(t, filepath.Join(f.panelBackup(), "stale-from-old-version.qml"))
	mustNotExist(t, filepath.Join(f.panelDir(), "stale-from-old-version.qml"))
	assertPanelRuntimeOnly(t, f.panelDir())
}

func TestMakeInstallShellRestartHonoursLock(t *testing.T) {
	f := newInstallFixture(t)
	f.flag("locked", false)
	out := f.mustMake("install")
	t.Log(out)
	if !strings.Contains(f.calls(), "omarchy-restart-shell") {
		t.Errorf("unlocked session: shell restart not invoked:\n%s", f.calls())
	}
	_ = out

	f = newInstallFixture(t)
	f.flag("locked", true)
	out = f.mustMake("install")
	if strings.Contains(f.calls(), "omarchy-restart-shell") {
		t.Errorf("locked session: shell restart was invoked:\n%s", f.calls())
	}
	if !strings.Contains(out, "omarchy restart shell") {
		t.Errorf("locked session: output does not tell the user to run `omarchy restart shell`:\n%s", out)
	}
}

func TestMakeInstallRefusesForeignPanel(t *testing.T) {
	f := newInstallFixture(t)
	if err := os.MkdirAll(f.panelDir(), 0o755); err != nil { //nolint:gosec // fixture path under t.TempDir
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.panelDir(), "manifest.json"), []byte(`{"id": "someone.else"}`), 0o644); err != nil { //nolint:gosec // fixture path under t.TempDir
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.panelDir(), "mine.txt"), []byte("keep"), 0o644); err != nil { //nolint:gosec // fixture path under t.TempDir
		t.Fatal(err)
	}
	before := treeMap(t, f.panelDir())

	out, err := f.make("install")
	if err == nil {
		t.Fatalf("install over a foreign panel dir succeeded:\n%s", out)
	}
	if !strings.Contains(out, f.panelDir()) {
		t.Errorf("failure does not name %s:\n%s", f.panelDir(), out)
	}
	after := treeMap(t, f.panelDir())
	if len(before) != len(after) {
		t.Errorf("foreign dir changed: %v -> %v", before, after)
	}
	for k, v := range before {
		if after[k] != v {
			t.Errorf("foreign dir file %s changed", k)
		}
	}
	mustNotExist(t, f.panelBackup())
}

func TestMakeInstallLeavesHyprlandLuaAlone(t *testing.T) {
	f := newInstallFixture(t)
	hl := f.path(".config", "hypr", "hyprland.lua")
	if err := os.MkdirAll(filepath.Dir(hl), 0o755); err != nil { //nolint:gosec // fixture path under t.TempDir
		t.Fatal(err)
	}
	orig := []byte("-- my config\nrequire(\"hypr.other\")\n")
	if err := os.WriteFile(hl, orig, 0o644); err != nil { //nolint:gosec // fixture path under t.TempDir
		t.Fatal(err)
	}
	out := f.mustMake("install")
	t.Log(out)
	got, _ := os.ReadFile(hl) //nolint:gosec // fixture path under t.TempDir
	if !bytes.Equal(got, orig) {
		t.Errorf("hyprland.lua was modified:\n%s", got)
	}
	// The printed line is the one the file's own header names.
	header, _ := os.ReadFile("hyprland/backstory.lua")
	line := `require("hypr.backstory")`
	if !strings.Contains(string(header), line) {
		t.Errorf("hyprland/backstory.lua header does not name %s", line)
	}
	if !strings.Contains(out, "    "+line+"\n") {
		t.Errorf("exact line to add not printed:\n%s", out)
	}
}

func TestMakeUninstall(t *testing.T) {
	f := newInstallFixture(t)
	f.mustMake("install")
	f.flag("active", true)
	f.mustMake("install") // leaves .prev and a panel backup to clean too
	store := f.path(".local", "share", "backstory")
	if err := os.MkdirAll(store, 0o700); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(store, "backstory.db")
	if err := os.WriteFile(db, []byte("memory"), 0o600); err != nil {
		t.Fatal(err)
	}
	// An opted-in sync timer (`backstory install sync`) is on disk too.
	for _, n := range []string{"backstory-sync.service", "backstory-sync.timer"} {
		if err := os.WriteFile(f.path(".config", "systemd", "user", n), []byte("[Unit]\n"), 0o644); err != nil { //nolint:gosec // fixture
			t.Fatal(err)
		}
	}
	f.resetCalls()
	f.mustMake("uninstall")

	mustNotExist(t, f.path(".config", "systemd", "user", "backstory-sync.service"))
	mustNotExist(t, f.path(".config", "systemd", "user", "backstory-sync.timer"))
	if !strings.Contains(f.calls(), "systemctl --user disable --now backstory-sync.timer") {
		t.Errorf("sync timer not disabled:\n%s", f.calls())
	}
	mustNotExist(t, f.path(".local", "bin", "backstory"))
	mustNotExist(t, f.path(".local", "bin", "backstory.prev"))
	mustNotExist(t, f.path(".config", "systemd", "user", "backstory.service"))
	mustNotExist(t, f.panelDir())
	mustNotExist(t, f.panelBackup())
	mustNotExist(t, f.path(".config", "hypr", "backstory.lua"))
	calls := f.calls()
	for _, w := range []string{"systemctl --user disable backstory", "systemctl --user stop backstory"} {
		if !strings.Contains(calls, w) {
			t.Errorf("service-manager log lacks %q:\n%s", w, calls)
		}
	}
	if b, err := os.ReadFile(db); err != nil || string(b) != "memory" { //nolint:gosec // fixture path under t.TempDir
		t.Errorf("store was touched: %v", err)
	}
}

// TestRepoShipsBackstoryIgnoreAndReadmeDocumentsIt: a fresh clone must never be
// captured by shell capture, so the marker is tracked at the repo root and the
// README documents it.
func TestRepoShipsBackstoryIgnoreAndReadmeDocumentsIt(t *testing.T) {
	root := ".."
	if _, err := os.Stat(filepath.Join(root, ".backstory-ignore")); err != nil {
		t.Fatalf("repo root .backstory-ignore: %v", err)
	}
	out, err := exec.Command("git", "-C", root, "ls-files", "--error-unmatch", ".backstory-ignore").CombinedOutput()
	if err != nil {
		t.Fatalf(".backstory-ignore is not tracked in git: %v\n%s", err, out)
	}
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), ".backstory-ignore") {
		t.Error("README.md does not mention .backstory-ignore")
	}
}

// panelRuntimeFiles lists what the panel loads at runtime: the manifest, its
// entry points, every QML type and the JS under js/.
func panelRuntimeFiles(t *testing.T) []string {
	t.Helper()
	var want []string
	for _, pat := range []string{"manifest.json", "*.qml", "js/*.js"} {
		m, err := filepath.Glob(filepath.Join("..", "panel", pat))
		if err != nil || len(m) == 0 {
			t.Fatalf("glob %s: %v (%d matches)", pat, err, len(m))
		}
		for _, p := range m {
			rel, _ := filepath.Rel(filepath.Join("..", "panel"), p)
			want = append(want, rel)
		}
	}
	return want
}

// assertPanelRuntimeOnly checks the installed panel holds every runtime file
// byte-for-byte and no test or fixture file (tst_*, anything under tests/,
// qmltest/ or testdata/, or *_test.go).
func assertPanelRuntimeOnly(t *testing.T, dir string) {
	t.Helper()
	for _, rel := range panelRuntimeFiles(t) {
		want, err := os.ReadFile(filepath.Join("..", "panel", rel)) //nolint:gosec // repo path
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dir, rel)) //nolint:gosec // fixture path
		if err != nil {
			t.Errorf("installed panel lacks runtime file %s: %v", rel, err)
		} else if string(got) != string(want) {
			t.Errorf("installed %s differs from source", rel)
		}
	}
	for rel := range treeMap(t, dir) {
		parts := strings.Split(filepath.ToSlash(rel), "/")
		bad := strings.HasPrefix(parts[len(parts)-1], "tst_") || strings.HasSuffix(rel, "_test.go")
		for _, p := range parts[:len(parts)-1] {
			if p == "tests" || p == "qmltest" || p == "testdata" {
				bad = true
			}
		}
		if bad {
			t.Errorf("installed panel contains test/fixture file %s", rel)
		}
	}
}

const enableCall = "omarchy plugin enable ridgetopai.backstory"

// fakeOmarchy records each call and whether the plugin directory existed then,
// and keeps the enabled state `plugin list --json` reports. The file
// "enable-ignored" holds how many `plugin enable ridgetopai.backstory` calls
// still report success without enabling (the shell restart undoing them).
func (f *installFixture) fakeOmarchy() {
	f.fake("omarchy", `if [ -d "$XDG_CONFIG_HOME/omarchy/plugins/ridgetopai.backstory" ]; then d=present; else d=absent; fi
state="$FAKE_LOG.plugin-enabled"; ignore="$FAKE_LOG.enable-ignored"
case "$1 $2 $3" in
"plugin list --json")
	echo "omarchy $* [plugin-dir $d]" >> "$FAKE_LOG"
	if [ -e "$state" ]; then e=true; else e=false; fi
	echo "[{\"id\":\"other.plugin\",\"enabled\":true},{\"id\":\"ridgetopai.backstory\",\"enabled\":$e}]"
	exit 0;;
esac
echo "omarchy $* [plugin-dir $d]" >> "$FAKE_LOG"
if [ "$1 $2 $3" = "plugin enable ridgetopai.backstory" ]; then
	n=$(cat "$ignore" 2>/dev/null || echo 0)
	if [ "$n" -gt 0 ]; then echo $((n-1)) > "$ignore"; else : > "$state"; fi
fi
if [ "$1 $2 $3" = "plugin disable ridgetopai.backstory" ]; then rm -f "$state"; fi`)
}

// enableIgnored makes the next n enable calls report success but not stick.
func (f *installFixture) enableIgnored(n int) {
	f.t.Helper()
	if err := os.WriteFile(f.log+".enable-ignored", []byte(strconv.Itoa(n)+"\n"), 0o644); err != nil { //nolint:gosec // fixture path under t.TempDir
		f.t.Fatal(err)
	}
}

// hostTools links every executable on the runner's PATH into dir, the first
// PATH entry winning, except the omarchy* tools. Since omarchy 4.5 they sit in
// /usr/bin beside coreutils, so no PATH entry can be dropped whole; and a
// fixture that reached the real lock probe or CLI would test the runner's
// session instead of its fakes.
func hostTools(t *testing.T, dir string) {
	t.Helper()
	seen := map[string]bool{}
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if !filepath.IsAbs(d) {
			continue
		}
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if seen[name] || strings.HasPrefix(name, "omarchy") {
				continue
			}
			seen[name] = true
			if err := os.Symlink(filepath.Join(d, name), filepath.Join(dir, name)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestInstallEnablesPanelPluginBeforeShellRestart(t *testing.T) {
	f := newInstallFixture(t)
	f.fakeOmarchy()
	f.mustMake("install")
	calls := f.calls()
	en := strings.Index(calls, enableCall+" [plugin-dir present]")
	if en < 0 {
		t.Fatalf("no enable call after the plugin directory existed:\n%s", calls)
	}
	rs := strings.Index(calls, "omarchy-restart-shell")
	if rs < 0 || en > rs {
		t.Errorf("enable must come before the shell restart:\n%s", calls)
	}
}

func TestInstallTwiceEnablesEachTime(t *testing.T) {
	f := newInstallFixture(t)
	f.fakeOmarchy()
	f.mustMake("install")
	f.flag("active", true)
	f.mustMake("install")
	if n := strings.Count(f.calls(), enableCall); n != 2 {
		t.Errorf("enable recorded %d times, want 2:\n%s", n, f.calls())
	}
}

func TestInstallWithoutOmarchyCLIStillSucceedsAndNamesCommand(t *testing.T) {
	f := newInstallFixture(t)
	out := f.mustMake("install")
	if !strings.Contains(out, "plugin enable ridgetopai.backstory") {
		t.Errorf("output does not name the enable command:\n%s", out)
	}
}

// lockProbe fakes omarchy-hyprland-session-locked: exit 0 = locked while the
// "omarchy-locked" flag exists. The restart fake refuses (exit 1) when locked,
// as the real one does.
func (f *installFixture) lockProbe() {
	f.fake("omarchy-hyprland-session-locked", `[ -e "$(dirname "$FAKE_LOG")/omarchy-locked" ]`)
	f.fake("omarchy-restart-shell", `echo "omarchy-restart-shell" >> "$FAKE_LOG"
if [ -e "$(dirname "$FAKE_LOG")/omarchy-locked" ]; then echo "Refusing to restart Omarchy shell while the session is locked."; exit 1; fi`)
}

// withHarness gives `backstory install` a Claude harness to wire up.
func (f *installFixture) withHarness() {
	f.t.Helper()
	if err := os.MkdirAll(f.path(".claude"), 0o755); err != nil { //nolint:gosec // fixture path under t.TempDir
		f.t.Fatal(err)
	}
}

func countLines(s, sub string) int {
	n := 0
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) == sub || strings.HasPrefix(strings.TrimSpace(l), sub+" ") {
			n++
		}
	}
	return n
}

func TestInstallOmarchyLockedSessionDoesNotAbort(t *testing.T) {
	f := newInstallFixture(t)
	f.withHarness()
	f.lockProbe()
	f.flag("omarchy-locked", true) // locked, and pgrep sees no hyprlock
	out, err := f.make("install")
	if err != nil {
		t.Fatalf("make install failed on a locked session: %v\n%s", err, out)
	}
	if countLines(f.calls(), "omarchy-restart-shell") > 0 {
		t.Errorf("shell restarted while locked:\n%s", f.calls())
	}
	mustExist(t, filepath.Join(f.home, ".claude", "settings.json"))
	if !strings.Contains(out, "the panel loads when you unlock") {
		t.Errorf("missing deferred-restart line:\n%s", out)
	}
}

func TestInstallLockedThenUnlockedRestartsOnceAndDoesNotStack(t *testing.T) {
	f := newInstallFixture(t)
	f.withHarness()
	f.lockProbe()
	f.runDeferredUnit()
	f.extra = append(f.extra, "BACKSTORY_RESTART_POLL=1")
	f.flag("omarchy-locked", true)
	f.mustMake("install")
	f.mustMake("install") // a second install while pending replaces the unit
	if n := countLines(f.calls(), "systemd-run"); n != 2 {
		t.Fatalf("systemd-run calls = %d, want 2:\n%s", n, f.calls())
	}
	if !strings.Contains(f.calls(), "stop backstory-shell-restart") {
		t.Errorf("pending unit not stopped before replacement:\n%s", f.calls())
	}
	if countLines(f.calls(), "omarchy-restart-shell") > 0 {
		t.Fatalf("restart ran while locked:\n%s", f.calls())
	}
	f.flag("omarchy-locked", false)
	deadline := time.Now().Add(20 * time.Second)
	for countLines(f.calls(), "omarchy-restart-shell") == 0 && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
	time.Sleep(3 * time.Second) // a stacked second waiter would have fired by now
	if n := countLines(f.calls(), "omarchy-restart-shell"); n != 1 {
		t.Errorf("omarchy-restart-shell calls = %d, want exactly 1:\n%s", n, f.calls())
	}
}

func TestInstallShellRestartFailureIsNotFatal(t *testing.T) {
	f := newInstallFixture(t)
	f.withHarness()
	f.fake("omarchy-hyprland-session-locked", `exit 1`) // unlocked
	f.fake("omarchy-restart-shell", `echo "omarchy-restart-shell" >> "$FAKE_LOG"; exit 1`)
	out, err := f.make("install")
	if err != nil {
		t.Fatalf("make install failed when the shell restart failed: %v\n%s", err, out)
	}
	mustExist(t, filepath.Join(f.home, ".claude", "settings.json"))
	if !strings.Contains(out, "run `omarchy restart shell`") {
		t.Errorf("missing manual line:\n%s", out)
	}
}

// sshLockProbe fakes the real probe's behaviour over ssh: exit 2 (undetermined)
// when HYPRLAND_INSTANCE_SIGNATURE is unset, and with the signature "sig-new"
// exit 0 while the "omarchy-locked" flag exists, else 1. A stale instance dir
// "sig-old" is created too, older than "sig-new".
func (f *installFixture) sshLockProbe() {
	f.t.Helper()
	hypr := filepath.Join(f.run, "hypr")
	for i, d := range []string{"sig-old", "sig-new"} {
		p := filepath.Join(hypr, d)
		if err := os.MkdirAll(p, 0o755); err != nil { //nolint:gosec // fixture path under t.TempDir
			f.t.Fatal(err)
		}
		at := time.Now().Add(time.Duration(i-10) * time.Minute)
		if err := os.Chtimes(p, at, at); err != nil {
			f.t.Fatal(err)
		}
	}
	f.fake("omarchy-hyprland-session-locked", `[ -n "$HYPRLAND_INSTANCE_SIGNATURE" ] || exit 2
[ "$HYPRLAND_INSTANCE_SIGNATURE" = sig-new ] || exit 2
[ -e "$(dirname "$FAKE_LOG")/omarchy-locked" ]`)
	f.fake("omarchy-restart-shell", `echo "omarchy-restart-shell" >> "$FAKE_LOG"`)
}

func TestInstallDerivesHyprlandSignatureOverSSHAndDefers(t *testing.T) {
	f := newInstallFixture(t)
	f.withHarness()
	f.sshLockProbe()
	f.flag("omarchy-locked", true)
	out, err := f.make("install")
	if err != nil {
		t.Fatalf("make install failed: %v\n%s", err, out)
	}
	if countLines(f.calls(), "omarchy-restart-shell") > 0 {
		t.Errorf("shell restarted while locked:\n%s", f.calls())
	}
	if countLines(f.calls(), "systemd-run") != 1 {
		t.Errorf("restart not deferred to a unit:\n%s", f.calls())
	}
	if !strings.Contains(out, "the panel loads when you unlock") {
		t.Errorf("missing deferred-restart line:\n%s", out)
	}
}

func TestInstallUndeterminedProbeDefersAndUnlockedRestarts(t *testing.T) {
	f := newInstallFixture(t)
	f.withHarness()
	f.fake("omarchy-hyprland-session-locked", `exit 2`)
	out, err := f.make("install")
	if err != nil {
		t.Fatalf("make install failed: %v\n%s", err, out)
	}
	if countLines(f.calls(), "omarchy-restart-shell") > 0 || countLines(f.calls(), "systemd-run") != 1 {
		t.Errorf("undetermined probe must defer, not restart:\n%s", f.calls())
	}

	g := newInstallFixture(t)
	g.withHarness()
	g.fake("omarchy-hyprland-session-locked", `exit 1`)
	g.mustMake("install")
	if countLines(g.calls(), "omarchy-restart-shell") != 1 || countLines(g.calls(), "systemd-run") != 0 {
		t.Errorf("definitely unlocked must restart immediately:\n%s", g.calls())
	}
}

func TestDeferredWaiterDerivesSignatureAndRestartsOnceOnExit1(t *testing.T) {
	f := newInstallFixture(t)
	f.withHarness()
	f.sshLockProbe()
	f.runDeferredUnit()
	f.extra = append(f.extra, "BACKSTORY_RESTART_POLL=1")
	f.flag("omarchy-locked", true)
	f.mustMake("install")
	time.Sleep(3 * time.Second) // several polls while locked
	if n := countLines(f.calls(), "omarchy-restart-shell"); n != 0 {
		t.Fatalf("waiter restarted while locked (%d):\n%s", n, f.calls())
	}
	f.flag("omarchy-locked", false)
	deadline := time.Now().Add(20 * time.Second)
	for countLines(f.calls(), "omarchy-restart-shell") == 0 && time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
	time.Sleep(3 * time.Second)
	if n := countLines(f.calls(), "omarchy-restart-shell"); n != 1 {
		t.Errorf("omarchy-restart-shell calls = %d, want exactly 1:\n%s", n, f.calls())
	}
}

// TestFixtureInstallLeavesDecoyXDGDirsEmpty is the guard for issue #8: with the
// parent env's XDG_STATE_HOME and XDG_CONFIG_HOME (and the other XDG dirs)
// aimed at a decoy, the fixture install and the status control must write
// nothing there. Before the fix the installed daemon's usage file landed in
// $XDG_STATE_HOME/omarchy/agents/usage/.
func TestFixtureInstallLeavesDecoyXDGDirsEmpty(t *testing.T) {
	// resolve the Go dirs before the decoy exists: the default GOCACHE derives
	// from XDG_CACHE_HOME, and the build must not be pointed into the decoy.
	for _, k := range []string{"GOCACHE", "GOMODCACHE"} {
		t.Setenv(k, goEnv(t, k))
	}
	decoy := t.TempDir()
	for _, k := range []string{"XDG_STATE_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME"} {
		d := filepath.Join(decoy, k)
		if err := os.MkdirAll(d, 0o755); err != nil { //nolint:gosec // decoy path under t.TempDir
			t.Fatal(err)
		}
		t.Setenv(k, d)
	}
	f := newInstallFixture(t)
	f.mustMake("install")
	if out, err := f.status(t.TempDir()); err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	_ = filepath.WalkDir(decoy, func(p string, d fs.DirEntry, err error) error {
		if err == nil && p != decoy && !d.IsDir() {
			t.Errorf("fixture wrote into the developer's XDG dir: %s", p)
		} else if err == nil && p != decoy && filepath.Dir(p) != decoy {
			t.Errorf("fixture created %s under the developer's XDG dir", p)
		}
		return nil
	})
}

// announceStub makes the fake `name` print "STUB <name> <args>" to stdout
// before it runs its recorded body, so the captured install output carries
// the moment each stub was called and can be ordered against the doing lines.
func (f *installFixture) announceStub(name string) {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join(f.fakes, name)) //nolint:gosec // fixture path under t.TempDir
	if err != nil {
		f.t.Fatal(err)
	}
	body := strings.TrimPrefix(string(b), "#!/bin/sh\n")
	f.fake(name, `echo "STUB `+name+` $*"`+"\n"+body)
}

// announceGo points GO at a wrapper that prints "STUB go build" before the
// real go builds.
func (f *installFixture) announceGo() {
	f.t.Helper()
	realGo, err := exec.LookPath("go")
	if err != nil {
		f.t.Fatal(err)
	}
	f.fake("go-announce", `[ "$1" = build ] && echo "STUB go build"
exec "`+realGo+`" "$@"`)
	f.extra = append(f.extra, "GO="+filepath.Join(f.fakes, "go-announce"))
}

func indexOf(t *testing.T, out, sub string) int {
	t.Helper()
	i := strings.Index(out, sub)
	if i < 0 {
		t.Fatalf("output lacks %q:\n%s", sub, out)
	}
	return i
}

// TestInstallPrintsDoingLinesBeforeEachSlowStep: every slow step announces
// itself before the stub for that step is called, on a first install and on
// an upgrade (restart path).
func TestInstallPrintsDoingLinesBeforeEachSlowStep(t *testing.T) {
	f := newInstallFixture(t)
	f.withHarness()
	f.announceGo()
	for _, n := range []string{"systemctl", "omarchy-restart-shell"} {
		f.announceStub(n)
	}
	first := f.mustMake("install")
	upgrade := f.mustMake("install")

	steps := []struct{ doing, stub string }{
		{"building backstory (first build downloads Go modules, can take a minute)...", "STUB go build"},
		{"restarting the Omarchy shell...", "STUB omarchy-restart-shell"},
	}
	for _, out := range []string{first, upgrade} {
		for _, st := range steps {
			if d, s := indexOf(t, out, st.doing), indexOf(t, out, st.stub); d >= s {
				t.Errorf("%q printed after %q:\n%s", st.doing, st.stub, out)
			}
		}
		if a, b := indexOf(t, out, "waiting for the daemon to answer..."), indexOf(t, out, "connecting your agents..."); a >= b {
			t.Errorf("connecting agents line before the daemon wait:\n%s", out)
		}
		// `backstory install` is the real binary: its first harness line marks
		// the call, and the connecting line must precede it.
		c, h := indexOf(t, out, "connecting your agents..."), len(out)
		for _, m := range []string{"\nclaude: ", "\ncodex: ", "\nhermes: ", "\npi: "} {
			if i := strings.Index(out, m); i >= 0 && i < h {
				h = i
			}
		}
		if h == len(out) || c >= h {
			t.Errorf("connecting agents line not before the backstory install output:\n%s", out)
		}
		// The wait's own is-active poll is the last service-manager call.
		if d, s := indexOf(t, out, "waiting for the daemon to answer..."), strings.LastIndex(out, "STUB systemctl --user is-active"); d >= s {
			t.Errorf("daemon wait line printed after its is-active poll:\n%s", out)
		}
	}
	if d, s := indexOf(t, first, "starting backstory.service..."), indexOf(t, first, "STUB systemctl --user enable --now backstory"); d >= s {
		t.Errorf("start line printed after the enable call:\n%s", first)
	}
	if d, s := indexOf(t, upgrade, "restarting backstory.service..."), indexOf(t, upgrade, "STUB systemctl --user restart backstory"); d >= s {
		t.Errorf("restart line printed after the restart call:\n%s", upgrade)
	}
	if !strings.Contains(first, "installed ") || !strings.Contains(upgrade, "restarted backstory.service") || !strings.Contains(upgrade, "restarted the Omarchy shell") {
		t.Errorf("existing done lines changed:\n%s\n%s", first, upgrade)
	}
	// Plain style: new lines carry no ANSI escapes and no em dashes.
	for _, out := range []string{first, upgrade} {
		for _, line := range strings.Split(out, "\n") {
			if strings.HasSuffix(line, "...") && (strings.Contains(line, "\x1b") || strings.Contains(line, "\u2014")) {
				t.Errorf("doing line has ANSI or an em dash: %q", line)
			}
		}
		if strings.Contains(out, "\x1b[") {
			t.Errorf("install output has ANSI escapes:\n%q", out)
		}
	}
}

// TestInstallRestartLineAppearsWhileRestartIsStillRunning reads the output
// incrementally: the line must be out while the service-manager stub sleeps.
func TestInstallRestartLineAppearsWhileRestartIsStillRunning(t *testing.T) {
	f := newInstallFixture(t)
	f.mustMake("install") // unit now active: the next install restarts
	f.announceStub("systemctl")
	b, _ := os.ReadFile(filepath.Join(f.fakes, "systemctl"))
	slow := strings.Replace(string(b), "*\"enable --now\"*|*restart*)", "*\"enable --now\"*|*restart*)\n\tcase \"$*\" in *restart*) echo \"STUB sleeping\"; sleep 2 ;; esac", 1)
	if slow == string(b) {
		t.Fatal("systemctl stub has no restart branch to slow down")
	}
	f.fake("systemctl", strings.TrimPrefix(slow, "#!/bin/sh\n"))

	cmd := f.makeCmd("install")
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = pw.Close()
	var seen strings.Builder
	buf := make([]byte, 4096)
	sawLine, sawSleep := false, false
	for {
		n, rerr := pr.Read(buf)
		seen.WriteString(string(buf[:n]))
		if !sawLine && strings.Contains(seen.String(), "restarting backstory.service...") {
			sawLine = true
		}
		if strings.Contains(seen.String(), "STUB sleeping") {
			sawSleep = true
			break // the stub is mid-sleep; the doing line must already be here
		}
		if rerr != nil {
			break
		}
	}
	if !sawSleep {
		t.Fatalf("never saw the stub start sleeping:\n%s", seen.String())
	}
	if !sawLine {
		t.Errorf("restart line was not in the output while the restart stub was sleeping:\n%s", seen.String())
	}
	_, _ = bytes.NewBuffer(nil).ReadFrom(pr)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("make install: %v", err)
	}
}

func TestInstallHappyPathVerifiesOnceWithoutWarning(t *testing.T) {
	f := newInstallFixture(t)
	f.fakeOmarchy()
	out := f.mustMake("install")
	calls := f.calls()
	if n := strings.Count(calls, enableCall); n != 1 {
		t.Errorf("enable recorded %d times, want 1:\n%s", n, calls)
	}
	if n := strings.Count(calls, "omarchy plugin list --json"); n != 1 {
		t.Errorf("plugin list checked %d times, want 1:\n%s", n, calls)
	}
	if strings.Contains(out, "NOT ENABLED") || strings.Contains(out, "retrying") {
		t.Errorf("happy path printed a warning:\n%s", out)
	}
}

func TestInstallRetriesEnableOnceWhenFirstEnableDoesNotStick(t *testing.T) {
	f := newInstallFixture(t)
	f.fakeOmarchy()
	f.enableIgnored(1)
	f.extra = append(f.extra, "BACKSTORY_VERIFY_SETTLE=0")
	out := f.mustMake("install")
	calls := f.calls()
	if n := strings.Count(calls, enableCall); n != 2 {
		t.Errorf("enable recorded %d times, want 2 (initial + one retry):\n%s", n, calls)
	}
	if n := strings.Count(calls, "omarchy plugin list --json"); n != 2 {
		t.Errorf("plugin list checked %d times, want 2:\n%s", n, calls)
	}
	if rs, retry := strings.Index(calls, "omarchy-restart-shell"), strings.LastIndex(calls, enableCall); rs < 0 || retry < rs {
		t.Errorf("the retry must come after the shell restart:\n%s", calls)
	}
	if strings.Contains(out, "NOT ENABLED") {
		t.Errorf("recovered install printed the failure block:\n%s", out)
	}
}

func TestInstallFailsLoudlyWhenPanelStaysDisabled(t *testing.T) {
	f := newInstallFixture(t)
	f.fakeOmarchy()
	f.enableIgnored(5)
	f.extra = append(f.extra, "BACKSTORY_VERIFY_SETTLE=0")
	out, err := f.make("install")
	if err == nil {
		t.Fatalf("install exited 0 with the panel disabled:\n%s", out)
	}
	if n := strings.Count(f.calls(), enableCall); n != 2 {
		t.Errorf("enable recorded %d times, want exactly 2:\n%s", n, f.calls())
	}
	for _, want := range []string{"THE PANEL IS NOT ENABLED", "omarchy plugin enable ridgetopai.backstory"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// The block is the last thing printed, not a line mid-output.
	if i := strings.LastIndex(out, "==================================================================\n"); i < 0 || strings.Contains(out[i:], "installed ") || strings.Contains(out[i:], "next:") {
		t.Errorf("failure block is not final:\n%s", out)
	}
	if strings.Contains(out, "next: open the panel") {
		t.Errorf("failed install still printed the success hint:\n%s", out)
	}
}

func TestInstallCarriesLegacyBarSectionToTheNewPlugin(t *testing.T) {
	for _, section := range []string{"right", "left"} {
		t.Run(section, func(t *testing.T) {
			f := newInstallFixture(t)
			f.fakeOmarchy()
			legacy := filepath.Join(f.cfg, "omarchy", "plugins", "backstory.this-week")
			if err := os.MkdirAll(legacy, 0o755); err != nil { //nolint:gosec // fixture path under t.TempDir
				t.Fatal(err)
			}
			manifest := `{"id": "backstory.this-week"}`
			if err := os.WriteFile(filepath.Join(legacy, "manifest.json"), []byte(manifest), 0o644); err != nil { //nolint:gosec // fixture path
				t.Fatal(err)
			}
			shell := `{"bar":{"left":["omarchy.workspaces"],"center":[],"right":["omarchy.clock","backstory.this-week"]}}`
			if section == "left" {
				shell = `{"bar":{"left":["omarchy.workspaces","backstory.this-week"],"right":["omarchy.clock"]}}`
			}
			if err := os.WriteFile(filepath.Join(f.cfg, "omarchy", "shell.json"), []byte(shell), 0o644); err != nil { //nolint:gosec // fixture path
				t.Fatal(err)
			}
			f.mustMake("install")
			if want := enableCall + " --section " + section + " "; !strings.Contains(f.calls(), want) {
				t.Errorf("enable did not carry section %q:\n%s", section, f.calls())
			}
		})
	}
}

func TestInstallEnablesWithoutSectionWhenLegacyWasNotInTheBar(t *testing.T) {
	f := newInstallFixture(t)
	f.fakeOmarchy()
	if err := os.MkdirAll(filepath.Join(f.cfg, "omarchy"), 0o755); err != nil { //nolint:gosec // fixture path
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.cfg, "omarchy", "shell.json"), []byte(`{"bar":{"right":["omarchy.clock"]}}`), 0o644); err != nil { //nolint:gosec // fixture path
		t.Fatal(err)
	}
	f.mustMake("install")
	if strings.Contains(f.calls(), "--section") {
		t.Errorf("section passed although the legacy widget was not in the bar:\n%s", f.calls())
	}
}
