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
