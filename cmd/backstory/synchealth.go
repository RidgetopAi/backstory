package main

import (
	"os"
	"time"

	"github.com/RidgetopAi/backstory/internal/install"
	"github.com/RidgetopAi/backstory/internal/store"
	bsync "github.com/RidgetopAi/backstory/internal/sync"
)

// exitSyncFailing is the exit code of `backstory status` while sync is on and
// not syncing (documented in USER-MANUAL.md). It is distinct from 1 (daemon
// unreachable or any other error) so a script can tell the two apart.
const exitSyncFailing = 3

// syncTimerState observes the optional sync timer through the installer's
// overridable service-manager hook.
func syncTimerState() bsync.TimerState {
	home, err := os.UserHomeDir()
	if err != nil {
		return bsync.TimerAbsent
	}
	installed, active := install.SyncTimerActive(home)
	switch {
	case !installed:
		return bsync.TimerAbsent
	case active:
		return bsync.TimerActive
	}
	return bsync.TimerInactive
}

// syncHealth is sync.Health over st with the timer observed now.
func syncHealth(st *store.Store, now time.Time) (bsync.HealthReport, error) {
	return bsync.Check(st, syncTimerState(), now)
}
