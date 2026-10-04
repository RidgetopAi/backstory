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
// the test never touches the runner's real systemd or shell.
type installFixture struct {
	t      *testing.T
	home   string
	cfg    string
	fakes  string
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
		log:    filepath.Join(root, "calls.log"),
		binOut: filepath.Join(root, "build", "backstory"),
		run:    filepath.Join(root, "run"),
	}
	f.cfg = filepath.Join(f.home, ".config")
	for _, d := range []string{f.home, f.fakes} {
		if err := os.MkdirAll(d, 0o755); err != nil { //nolint:gosec // fixture path under t.TempDir
			t.Fatal(err)
		}
	}
	// enable --now / restart "start the unit": mark it active and run the
	// installed daemon detached (unless FAKE_NODAEMON), as systemd would.
	f.fake("systemctl", `echo "systemctl $*" >> "$FAKE_LOG"
pids="$FAKE_LOG.daemon-pids"
stopdaemon() { [ -f "$pids" ] && while read -r p; do kill "$p" 2>/dev/null; done < "$pids"; : > "$pids"; }
case "$*" in
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
	return f
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
	out, err := exec.Command("go", "env", key).Output() //nolint:gosec // fixture path under t.TempDir
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// make runs `make <target>` in the repo against the fixture and returns the
// combined output and the exit error.
func (f *installFixture) make(target string) (string, error) {
	f.t.Helper()
	cmd := exec.Command("make", "-C", "..", target, "BIN="+f.binOut) //nolint:gosec // fixture path under t.TempDir
	cmd.Env = append(os.Environ(),
		"HOME="+f.home,
		"XDG_CONFIG_HOME="+f.cfg,
		"XDG_DATA_HOME="+filepath.Join(f.home, ".local", "share"),
		"XDG_RUNTIME_DIR="+f.run,
		"BACKSTORY_HEALTH_TIMEOUT=10",
		"PATH="+f.fakes+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_LOG="+f.log,
		"FAKE_ACTIVE="+filepath.Join(filepath.Dir(f.log), "active"),
		"FAKE_LOCKED="+filepath.Join(filepath.Dir(f.log), "locked"),
		// keep the build off the (empty) fixture HOME
		"GOCACHE="+goEnv(f.t, "GOCACHE"),
		"GOMODCACHE="+goEnv(f.t, "GOMODCACHE"),
	)
	cmd.Env = append(cmd.Env, f.extra...)
	out, err := cmd.CombinedOutput()
	return string(out), err
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
	return filepath.Join(f.cfg, "omarchy", "plugins", "backstory.this-week")
}

func (f *installFixture) panelBackup() string {
	return filepath.Join(f.cfg, "omarchy", "plugins", ".backstory.this-week.prev")
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

func assertSameTree(t *testing.T, want, got string) {
	t.Helper()
	w, g := treeMap(t, want), treeMap(t, got)
	for k, v := range w {
		gv, ok := g[k]
		if !ok {
			t.Errorf("%s lacks %s", got, k)
		} else if gv != v {
			t.Errorf("%s differs from %s", filepath.Join(got, k), filepath.Join(want, k))
		}
	}
	for k := range g {
		if _, ok := w[k]; !ok {
			t.Errorf("%s has extra %s", got, k)
		}
	}
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
	assertSameTree(t, "../panel", f.panelDir())
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

func TestInstallerPrintsAddLineWhenMissingOrCommentedOut(t *testing.T) {
	for name, content := range map[string]string{
		"absent":        "-- my config\nrequire(\"hypr.other\")\n",
		"commented out": "-- require(\"hypr.backstory\")\n",
	} {
		t.Run(name, func(t *testing.T) {
			f := newInstallFixture(t)
			hl, orig := f.writeHyprland(content)
			out := f.mustMake("install")
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

// The installer runs from the clone dir. Its own backstory calls must leave
// no project row and no session for that dir in the user's store; a plain
// `backstory status` from the same dir (the control) does record one.
func TestInstallerRecordsNoProjectForBuildDir(t *testing.T) {
	f := newInstallFixture(t)
	f.mustMake("install")
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

	// Control: the same call without the installer's flag is recorded, so
	// the assertions above can fail.
	ctl := exec.Command(f.path(".local", "bin", "backstory"), "status") //nolint:gosec // fixture path under t.TempDir
	ctl.Dir = clone
	ctl.Env = append(os.Environ(), "HOME="+f.home, "XDG_RUNTIME_DIR="+f.run, "XDG_DATA_HOME="+filepath.Join(f.home, ".local", "share"))
	if out, err := ctl.CombinedOutput(); err != nil {
		t.Fatalf("control status: %v\n%s", err, out)
	}
	if sessionsAt() == 0 {
		t.Error("control: a plain `backstory status` from the clone dir recorded no session — the probe is not sensitive")
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
	assertSameTree(t, "../panel", f.panelDir())
}

func TestMakeInstallShellRestartHonoursLock(t *testing.T) {
	f := newInstallFixture(t)
	f.flag("locked", false)
	out := f.mustMake("install")
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
	f.resetCalls()
	f.mustMake("uninstall")

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
