package install

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// The optional sync timer (phase 8, D6): a systemd --user timer that runs
// `backstory sync --quiet`. It is never part of a bare `backstory install`.
const (
	SyncServiceName = "backstory-sync.service"
	SyncTimerName   = "backstory-sync.timer"
)

// SyncServiceUnit and SyncTimerUnit are the unit files; ops/ carries the same
// text for `make install`-less readers and a test keeps the two identical.
//
//go:embed units/backstory-sync.service
var SyncServiceUnit string

//go:embed units/backstory-sync.timer
var SyncTimerUnit string

// ServiceManager runs `systemctl --user <args>`. Tests replace it; nothing
// else may invoke a real service manager.
var ServiceManager = func(args ...string) error {
	out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).CombinedOutput() //nolint:gosec // fixed binary, fixed verbs
	if err != nil {
		return fmt.Errorf("systemctl --user %v: %w: %s", args, err, out)
	}
	return nil
}

// SyncUnitDir is where the user units live: $XDG_CONFIG_HOME/systemd/user,
// else <home>/.config/systemd/user (the same rule as ops/install.sh).
func SyncUnitDir(home string) string {
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(home, ".config")
	}
	return filepath.Join(cfg, "systemd", "user")
}

// InstallSyncTimer writes both units and enables the timer.
func InstallSyncTimer(home string) error {
	dir := SyncUnitDir(home)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // user unit dir
		return err
	}
	for name, body := range map[string]string{SyncServiceName: SyncServiceUnit, SyncTimerName: SyncTimerUnit} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil { //nolint:gosec // unit files are world-readable
			return err
		}
	}
	if err := ServiceManager("daemon-reload"); err != nil {
		return err
	}
	return ServiceManager("enable", "--now", SyncTimerName)
}

// RemoveSyncTimer disables the timer and removes both units; absent is fine.
func RemoveSyncTimer(home string) error {
	dir := SyncUnitDir(home)
	_, errT := os.Stat(filepath.Join(dir, SyncTimerName))
	_, errS := os.Stat(filepath.Join(dir, SyncServiceName))
	if errT != nil && errS != nil {
		return nil
	}
	_ = ServiceManager("disable", "--now", SyncTimerName) // best effort: it may not be enabled
	for _, name := range []string{SyncTimerName, SyncServiceName} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return ServiceManager("daemon-reload")
}

// SyncTimerStatus is present (both units written and the timer enabled),
// inactive (units written, timer not enabled) or absent.
func SyncTimerStatus(home string) ItemStatus {
	dir := SyncUnitDir(home)
	_, errT := os.Stat(filepath.Join(dir, SyncTimerName))
	_, errS := os.Stat(filepath.Join(dir, SyncServiceName))
	if errT != nil || errS != nil {
		return StatusAbsent
	}
	if err := ServiceManager("is-enabled", "--quiet", SyncTimerName); err != nil {
		return StatusInactive
	}
	return StatusPresent
}

// SyncTimerActive observes the timer for sync health: both unit files written
// and `systemctl --user is-active` failing means installed but inactive. It
// goes through ServiceManager, so tests override it. installed is false when
// the units are absent (not a failure).
func SyncTimerActive(home string) (installed, active bool) {
	dir := SyncUnitDir(home)
	_, errT := os.Stat(filepath.Join(dir, SyncTimerName))
	_, errS := os.Stat(filepath.Join(dir, SyncServiceName))
	if errT != nil || errS != nil {
		return false, false
	}
	return true, ServiceManager("is-active", "--quiet", SyncTimerName) == nil
}
