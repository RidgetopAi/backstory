package install

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The optional sync timer (phase 8, D6): a systemd --user timer that runs
// `backstory sync --quiet`. It is never part of a bare `backstory install`.
const (
	SyncServiceName = "backstory-sync.service"
	SyncTimerName   = "backstory-sync.timer"
)

// SyncServiceUnit and SyncTimerUnit are the unit files; ops/ carries the same
// text for `make install` readers and a test keeps the two identical.
// SyncServiceUnit names the make-install binary path (SyncUnitTemplateBinary);
// SyncServiceUnitFor renders it for the binary that is actually running.
//
//go:embed units/backstory-sync.service
var SyncServiceUnit string

//go:embed units/backstory-sync.timer
var SyncTimerUnit string

// SyncUnitTemplateBinary is the make-install ExecStart binary the embedded
// service unit names; it is the text SyncServiceUnitFor replaces.
const SyncUnitTemplateBinary = "%h/.local/bin/backstory"

// executablePath is this process's binary; tests replace it.
var executablePath = os.Executable

// SyncServiceUnitFor is the sync service unit with ExecStart pointing at exe.
func SyncServiceUnitFor(exe string) string {
	return strings.Replace(SyncServiceUnit, "ExecStart="+SyncUnitTemplateBinary+" ", "ExecStart="+systemdExecPath(exe)+" ", 1)
}

// systemdExecPath makes a path safe as the first word of ExecStart: `%` is a
// specifier escape and whitespace splits words, so the path is escaped/quoted.
func systemdExecPath(p string) string {
	p = strings.ReplaceAll(p, "%", "%%")
	if strings.ContainsAny(p, " \t\"'\\") {
		p = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(p) + `"`
	}
	return p
}

// runningSyncService renders the service unit for the running binary.
func runningSyncService() (string, error) {
	exe, err := executablePath()
	if err != nil {
		return "", fmt.Errorf("locate backstory binary: %w", err)
	}
	if abs, err := filepath.Abs(exe); err == nil {
		exe = abs
	}
	return SyncServiceUnitFor(exe), nil
}

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

// syncUnits pairs each unit file with its managedFile and the bytes install
// writes now.
func syncUnits(home string) ([]syncUnit, error) {
	svc, err := runningSyncService()
	if err != nil {
		return nil, err
	}
	dir := SyncUnitDir(home)
	return []syncUnit{
		{path: filepath.Join(dir, SyncTimerName), file: syncTimerFile, want: SyncTimerUnit},
		{path: filepath.Join(dir, SyncServiceName), file: syncServiceFile, want: svc},
	}, nil
}

type syncUnit struct {
	path string
	file managedFile
	want string
}

// status is the unit's managedFile status, except that a unit file that is
// a symlink (dangling or not) is the owner's, linked in from a dotfiles
// tree, as ops/install.sh treats it: unless it already holds what install
// would write it is edited, so install never writes through it.
func (u syncUnit) status() (ItemStatus, error) {
	st, err := u.file.status(u.path, u.want)
	if err == nil && st != StatusPresent && isSymlink(u.path) {
		return StatusEdited, nil
	}
	return st, err
}

// InstallSyncTimer writes each unit that is absent or a version Backstory
// shipped and enables the timer. A unit the owner edited or symlinked is
// kept as it is and returned in kept.
func InstallSyncTimer(home string) (kept []string, err error) {
	units, err := syncUnits(home)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(SyncUnitDir(home), 0o755); err != nil { //nolint:gosec // user unit dir
		return nil, err
	}
	for _, u := range units {
		st, err := u.status()
		if err != nil {
			return nil, err
		}
		switch st {
		case StatusEdited:
			kept = append(kept, u.path)
		case StatusAbsent, StatusOutdated:
			if err := os.WriteFile(u.path, []byte(u.want), 0o644); err != nil { //nolint:gosec // unit files are world-readable
				return nil, err
			}
		}
	}
	if err := ServiceManager("daemon-reload"); err != nil {
		return kept, err
	}
	return kept, ServiceManager("enable", "--now", SyncTimerName)
}

// RemoveSyncTimer disables the timer and removes each unit that holds a
// version Backstory shipped; absent is fine. A unit the owner edited is kept
// and returned in kept, and a unit file that is a symlink was linked there by
// the user (a dotfiles tree), not written by install, so it is left in place
// (symlink.go).
func RemoveSyncTimer(home string) (kept []string, err error) {
	dir := SyncUnitDir(home)
	_, errT := os.Stat(filepath.Join(dir, SyncTimerName))
	_, errS := os.Stat(filepath.Join(dir, SyncServiceName))
	if errT != nil && errS != nil {
		return nil, nil
	}
	units, err := syncUnits(home)
	if err != nil {
		return nil, err
	}
	_ = ServiceManager("disable", "--now", SyncTimerName) // best effort: it may not be enabled
	for _, u := range units {
		st, err := u.status()
		if err != nil {
			return kept, err
		}
		switch st {
		case StatusEdited:
			kept = append(kept, u.path)
		case StatusPresent, StatusOutdated:
			if err := removeOwnedWholeFile(u.path); err != nil && !os.IsNotExist(err) {
				return kept, err
			}
		}
	}
	return kept, ServiceManager("daemon-reload")
}

// SyncTimerStatus is absent (a unit missing), edited (a unit changed since
// Backstory wrote it; install and --remove keep it), outdated (a unit is an
// older shipped version, e.g. the service naming the old %h/.local/bin path;
// `backstory install sync` rewrites it), inactive (units written, timer not
// enabled) or present.
func SyncTimerStatus(home string) ItemStatus {
	units, err := syncUnits(home)
	if err != nil {
		return StatusAbsent
	}
	worst := StatusPresent
	for _, u := range units {
		st, err := u.status()
		if err != nil || st == StatusAbsent {
			return StatusAbsent
		}
		if st == StatusEdited || (st == StatusOutdated && worst == StatusPresent) {
			worst = st
		}
	}
	if worst != StatusPresent {
		return worst
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
