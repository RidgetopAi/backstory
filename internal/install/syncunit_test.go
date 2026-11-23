package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncServiceUnitForUsesGivenBinary(t *testing.T) {
	got := SyncServiceUnitFor("/tmp/x/backstory")
	if !strings.Contains(got, "\nExecStart=/tmp/x/backstory sync --quiet\n") || strings.Contains(got, "%h/.local/bin") {
		t.Fatalf("unit:\n%s", got)
	}
	got = SyncServiceUnitFor("/tmp/my 50%/backstory")
	if !strings.Contains(got, `ExecStart="/tmp/my 50%%/backstory" sync --quiet`) {
		t.Fatalf("unsafe path not escaped:\n%s", got)
	}
}

func TestInstallSyncTimerWritesRunningBinary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	oldSM, oldExe := ServiceManager, executablePath
	ServiceManager = func(...string) error { return nil }
	executablePath = func() (string, error) { return "/opt/tmp/backstory", nil }
	t.Cleanup(func() { ServiceManager, executablePath = oldSM, oldExe })

	if err := InstallSyncTimer(home); err != nil {
		t.Fatal(err)
	}
	svc := filepath.Join(SyncUnitDir(home), SyncServiceName)
	b, _ := os.ReadFile(svc) //nolint:gosec // temp dir
	if !strings.Contains(string(b), "ExecStart=/opt/tmp/backstory sync --quiet") {
		t.Fatalf("unit:\n%s", b)
	}
	if s := SyncTimerStatus(home); s != StatusPresent {
		t.Fatalf("status %s", s)
	}
	if err := os.WriteFile(svc, []byte(SyncServiceUnit), 0o644); err != nil { //nolint:gosec // temp dir
		t.Fatal(err)
	}
	if s := SyncTimerStatus(home); s != StatusOutdated {
		t.Fatalf("old %%h unit: status %s", s)
	}
}

// TestRemoveSyncTimerKeepsSymlinkedUnit: a unit file the user linked in from
// a dotfiles tree survives --remove (link and target); a regular unit file
// Backstory wrote is still deleted.
func TestRemoveSyncTimerKeepsSymlinkedUnit(t *testing.T) {
	home, dots := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	oldSM := ServiceManager
	ServiceManager = func(...string) error { return nil }
	t.Cleanup(func() { ServiceManager = oldSM })

	dir := SyncUnitDir(home)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dots, SyncTimerName)
	if err := os.WriteFile(target, []byte(SyncTimerUnit), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, SyncTimerName)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	svc := filepath.Join(dir, SyncServiceName)
	if err := os.WriteFile(svc, []byte(SyncServiceUnit), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := RemoveSyncTimer(home); err != nil {
		t.Fatalf("RemoveSyncTimer: %v", err)
	}
	if got, err := os.Readlink(link); err != nil || got != target {
		t.Errorf("symlinked timer unit: -> %q (%v), want it kept -> %q", got, err, target)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("timer unit target gone: %v", err)
	}
	if _, err := os.Lstat(svc); !os.IsNotExist(err) {
		t.Errorf("regular service unit survived remove (lstat err %v)", err)
	}
}
