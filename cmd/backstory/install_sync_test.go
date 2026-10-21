package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/install"
	bsync "github.com/RidgetopAi/backstory/internal/sync"
)

// fakeServiceManager replaces the installer's service-manager hook for one
// test: calls are recorded, none reaches a real systemctl.
func fakeServiceManager(t *testing.T, enabled *atomic.Bool) *[]string {
	t.Helper()
	var calls []string
	old := install.ServiceManager
	install.ServiceManager = func(args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		if len(args) > 0 && args[0] == "enable" {
			enabled.Store(true)
		}
		if len(args) > 0 && args[0] == "disable" {
			enabled.Store(false)
		}
		if len(args) > 0 && args[0] == "is-enabled" && !enabled.Load() {
			return os.ErrNotExist
		}
		return nil
	}
	t.Cleanup(func() { install.ServiceManager = old })
	return &calls
}

func syncUnitPaths(home string) (svc, timer string) {
	dir := filepath.Join(home, ".config", "systemd", "user")
	return filepath.Join(dir, "backstory-sync.service"), filepath.Join(dir, "backstory-sync.timer")
}

func TestInstallSyncLifecycle(t *testing.T) {
	bin := buildBackstory(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	var enabled atomic.Bool
	calls := fakeServiceManager(t, &enabled)
	svc, timer := syncUnitPaths(home)

	local, peer := newSyncHome(t, "/a/widgets"), newSyncHome(t, "/b/widgets")

	// Refused with no paired peer: one plain line, nothing written, nothing enabled.
	t.Setenv("XDG_DATA_HOME", local.xdg)
	code, _, errs := runLinkCLI(t, "install", "sync")
	if code == 0 || !strings.Contains(errs, "no paired computer") || strings.Count(strings.TrimSpace(errs), "\n") != 0 {
		t.Fatalf("unpaired: code=%d err=%q", code, errs)
	}
	for _, p := range []string{svc, timer} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("%s written without a peer", p)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("service manager used without a peer: %v", *calls)
	}

	// --check: absent.
	if code, out, _ := runLinkCLI(t, "install", "sync", "--check"); code == 0 || !strings.Contains(out, "absent") {
		t.Fatalf("check absent: code=%d out=%q", code, out)
	}

	useTransport(t, local, serveScript(bin, peer))
	if code, _, errs := runLinkCLI(t, "pair", "laptop"); code != 0 {
		t.Fatalf("pair: %s", errs)
	}

	code, out, errs := runLinkCLI(t, "install", "sync")
	if code != 0 {
		t.Fatalf("install sync: code=%d out=%q err=%q", code, out, errs)
	}
	for _, p := range []string{svc, timer} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("unit not written: %v", err)
		}
	}
	b, _ := os.ReadFile(svc) //nolint:gosec // fixture path
	if !strings.Contains(string(b), "ExecStart=%h/.local/bin/backstory sync --quiet") || !strings.Contains(string(b), "Type=oneshot") {
		t.Fatalf("service unit: %s", b)
	}
	if !strings.Contains(strings.Join(*calls, "\n"), "enable --now backstory-sync.timer") {
		t.Fatalf("timer not enabled via the hook: %v", *calls)
	}
	if code, out, _ := runLinkCLI(t, "install", "sync", "--check"); code != 0 || !strings.Contains(out, "present") {
		t.Fatalf("check present: code=%d out=%q", code, out)
	}

	// Inactive: units on disk, timer not enabled.
	enabled.Store(false)
	if code, out, _ := runLinkCLI(t, "install", "sync", "--check"); code == 0 || !strings.Contains(out, "inactive") {
		t.Fatalf("check inactive: code=%d out=%q", code, out)
	}
	enabled.Store(true)

	if code, _, errs := runLinkCLI(t, "install", "sync", "--remove"); code != 0 {
		t.Fatalf("remove: %s", errs)
	}
	for _, p := range []string{svc, timer} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("%s survived --remove", p)
		}
	}
	if !strings.Contains(strings.Join(*calls, "\n"), "disable --now backstory-sync.timer") {
		t.Fatalf("timer not disabled: %v", *calls)
	}
}

// TestBareInstallNeverTouchesSyncTimer: bare `backstory install` and its
// --remove neither write nor enable the sync timer.
func TestBareInstallNeverTouchesSyncTimer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	var enabled atomic.Bool
	calls := fakeServiceManager(t, &enabled)

	if code, _, errs := runLinkCLI(t, "install", "agents", "--no-verify"); code != 0 {
		t.Fatalf("install agents: %s", errs)
	}
	if code, _, errs := runLinkCLI(t, "install", "agents", "--remove"); code != 0 {
		t.Fatalf("remove agents: %s", errs)
	}
	svc, timer := syncUnitPaths(home)
	for _, p := range []string{svc, timer} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("bare install wrote %s", p)
		}
	}
	if len(*calls) != 0 || enabled.Load() {
		t.Fatalf("bare install used the service manager: %v", *calls)
	}
}

func TestSyncBusyWritesNoAttemptAndQuietIsSilent(t *testing.T) {
	bin := buildBackstory(t)
	local, peer := newSyncHome(t, "/a/widgets"), newSyncHome(t, "/b/widgets")
	useTransport(t, local, serveScript(bin, peer))
	if code, _, errs := runLinkCLI(t, "pair", "laptop"); code != 0 {
		t.Fatalf("pair: %s", errs)
	}

	// --quiet on success prints nothing at all.
	code, out, errs := runLinkCLI(t, "sync", "--quiet")
	if code != 0 || out != "" || errs != "" {
		t.Fatalf("quiet success: code=%d out=%q err=%q", code, out, errs)
	}
	attempts := func() int {
		st := local.open(t)
		defer func() { _ = st.Close() }()
		ps, err := st.SyncPeers()
		if err != nil || len(ps) != 1 {
			t.Fatalf("peers: %v %v", ps, err)
		}
		a, err := st.SyncAttempts(ps[0].Label(), 100)
		if err != nil {
			t.Fatal(err)
		}
		return len(a)
	}
	before := attempts()
	if before == 0 {
		t.Fatal("a real sync should have logged an attempt")
	}

	// A second sync while the first holds the lock reports busy and logs nothing.
	old := bsync.LockWait
	bsync.LockWait = 200 * time.Millisecond
	t.Cleanup(func() { bsync.LockWait = old })
	lock, err := bsync.AcquireLock(filepath.Join(local.xdg, "backstory"))
	if err != nil {
		t.Fatal(err)
	}
	code, out, errs = runLinkCLI(t, "sync")
	if code != 0 || !strings.Contains(out, "busy") || errs != "" {
		t.Fatalf("busy: code=%d out=%q err=%q", code, out, errs)
	}
	if code, out, errs = runLinkCLI(t, "sync", "--quiet"); code != 0 || out != "" || errs != "" {
		t.Fatalf("busy quiet: code=%d out=%q err=%q", code, out, errs)
	}
	if n := attempts(); n != before {
		t.Fatalf("busy sync logged attempts: %d -> %d", before, n)
	}
	lock.Release()
	if code, _, errs := runLinkCLI(t, "sync", "--quiet"); code != 0 || errs != "" {
		t.Fatalf("after release: code=%d err=%q", code, errs)
	}
	if n := attempts(); n != before+1 {
		t.Fatalf("sync after release: attempts %d -> %d", before, n)
	}
}

// TestSyncQuietPrintsOnlyFailureLines: failures still print and exit non-zero.
func TestSyncQuietPrintsOnlyFailureLines(t *testing.T) {
	bin := buildBackstory(t)
	local, peer := newSyncHome(t, "/a/widgets"), newSyncHome(t, "/b/widgets")
	useTransport(t, local, serveScript(bin, peer))
	if code, _, errs := runLinkCLI(t, "pair", "laptop"); code != 0 {
		t.Fatalf("pair: %s", errs)
	}
	useTransport(t, local, "exit 255")
	code, out, errs := runLinkCLI(t, "sync", "--quiet")
	if code == 0 || out != "" || !strings.HasPrefix(errs, "sync: ") || strings.Count(strings.TrimSpace(errs), "\n") != 0 {
		t.Fatalf("quiet failure: code=%d out=%q err=%q", code, out, errs)
	}
}

// TestSyncUnitsInOpsMatchEmbedded keeps ops/ and the installer's copy identical.
func TestSyncUnitsInOpsMatchEmbedded(t *testing.T) {
	for name, want := range map[string]string{
		"backstory-sync.service": install.SyncServiceUnit,
		"backstory-sync.timer":   install.SyncTimerUnit,
	} {
		b, err := os.ReadFile(filepath.Join("..", "..", "ops", name)) //nolint:gosec // repo path
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != want {
			t.Errorf("ops/%s differs from internal/install/units/%s", name, name)
		}
	}
	for _, w := range []string{"OnBootSec=2min", "OnUnitActiveSec=5min", "Persistent=true"} {
		if !strings.Contains(install.SyncTimerUnit, w) {
			t.Errorf("timer lacks %s", w)
		}
	}
}
