package sync

import (
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// Health is the one rule every loud surface shares (decision 2ac4c54f, D8:
// "fail loudly if not sync'd and user has sync on"): `status`, NEEDS YOU, the
// SessionStart block and the timer's notification all read it.

// SettingUnreachableHours is the settings key (hours, human-set) that
// overrides DefaultUnreachableThreshold.
const SettingUnreachableHours = "sync_unreachable_hours"

// DefaultUnreachableThreshold is how long recorded unreachable attempts may
// span, since the last ok, before a peer counts as failing.
const DefaultUnreachableThreshold = 24 * time.Hour

// SettingNotifiedState is the settings key (local to this device) holding
// whether the timer's notifier last announced failing or ok.
const SettingNotifiedState = "sync_notified_state"

// TimerState is what was observed of the optional sync timer.
type TimerState int

const (
	// TimerAbsent: not installed (or not observable). Never a failure.
	TimerAbsent TimerState = iota
	// TimerActive: installed and `systemctl --user is-active`.
	TimerActive
	// TimerInactive: installed but observed not active.
	TimerInactive
)

// PeerState is one peer's standing.
type PeerState string

const (
	// StateOK: the latest attempt was ok, or a non-failing short streak.
	StateOK PeerState = "ok"
	// StateFailing: see the rule on Health.
	StateFailing PeerState = "failing"
	// StatePaused: the latest attempt found capture off here or on the peer.
	StatePaused PeerState = "paused"
	// StateNeverSynced: enabled but no attempt exists. Not failing.
	StateNeverSynced PeerState = "never-synced"
)

// PeerHealth is Health's verdict on one enabled peer.
type PeerHealth struct {
	Peer  store.SyncPeerRow
	State PeerState
	// Reason is one plain line naming the cause; set when failing.
	Reason string
	// HasOK and LastOK: the peer's latest ok attempt.
	HasOK  bool
	LastOK time.Time
	// EvidenceIDs are the sync_attempts row ids behind a failing verdict
	// (the error row, or the unreachable streak), oldest first.
	EvidenceIDs []int64
}

// Failing reports State == StateFailing.
func (h PeerHealth) Failing() bool { return h.State == StateFailing }

// LastOKText is the last-ok time for a person, or "never".
func (h PeerHealth) LastOKText() string {
	if !h.HasOK {
		return "never"
	}
	return h.LastOK.Local().Format("2006-01-02 15:04")
}

// HealthReport is Health's verdict for the whole store.
type HealthReport struct {
	// On: at least one enabled peer exists.
	On    bool
	Peers []PeerHealth
}

// Failing returns the failing peers.
func (r HealthReport) Failing() []PeerHealth {
	var out []PeerHealth
	for _, p := range r.Peers {
		if p.Failing() {
			out = append(out, p)
		}
	}
	return out
}

// IsFailing reports whether any peer is failing.
func (r HealthReport) IsFailing() bool { return len(r.Failing()) > 0 }

// Health judges every enabled peer. attempts are the sync_attempts rows (any
// order, any peers; only rows naming a peer by device id or label count). A
// peer FAILS when any holds:
//
//	(a) its latest attempt's result is error;
//	(b) the attempts since its last ok include unreachable ones whose
//	    recorded times span more than threshold (counted attempts, never bare
//	    elapsed time);
//	(c) the timer is installed but observed not active.
//
// Everything else (ok, a short unreachable streak, paused) is not failing. A
// peer with no attempt at all is never-synced and never failing: an absent
// attempt is not evidence. Disabled peers are ignored; with no enabled peer
// sync is off. now is the reference instant for callers' wording; the rule
// itself reads only recorded attempt times.
func Health(peers []store.SyncPeerRow, attempts []store.SyncAttempt, timer TimerState, now time.Time, threshold time.Duration) HealthReport {
	_ = now
	var rep HealthReport
	for _, p := range peers {
		if !p.Enabled {
			continue
		}
		rep.On = true
		rep.Peers = append(rep.Peers, judge(p, attemptsOf(p, attempts), timer, threshold))
	}
	return rep
}

// attemptsOf selects p's attempts, oldest first (by time, then id).
func attemptsOf(p store.SyncPeerRow, all []store.SyncAttempt) []store.SyncAttempt {
	var out []store.SyncAttempt
	for _, a := range all {
		if (a.DeviceID != "" && a.DeviceID == p.DeviceID) || a.Peer == p.Label() {
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].TS.Equal(out[j].TS) {
			return out[i].TS.Before(out[j].TS)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func judge(p store.SyncPeerRow, atts []store.SyncAttempt, timer TimerState, threshold time.Duration) PeerHealth {
	h := PeerHealth{Peer: p, State: StateOK}
	if len(atts) == 0 {
		h.State = StateNeverSynced
		return h
	}
	last := atts[len(atts)-1]
	lastOKIdx := -1
	for i, a := range atts {
		if a.Result == store.SyncResultOK {
			lastOKIdx = i
		}
	}
	if lastOKIdx >= 0 {
		h.HasOK, h.LastOK = true, atts[lastOKIdx].TS
	}
	streak := atts[lastOKIdx+1:]
	var unreachable []store.SyncAttempt
	for _, a := range streak {
		if a.Result == store.SyncResultUnreachable {
			unreachable = append(unreachable, a)
		}
	}

	switch {
	case last.Result == store.SyncResultError:
		h.State = StateFailing
		h.Reason = orUnknown(last.Detail)
		h.EvidenceIDs = []int64{last.ID}
	case len(unreachable) > 1 && unreachable[len(unreachable)-1].TS.Sub(unreachable[0].TS) > threshold:
		h.State = StateFailing
		span := unreachable[len(unreachable)-1].TS.Sub(unreachable[0].TS)
		h.Reason = fmt.Sprintf("unreachable for %s across %d attempts", roundDuration(span), len(unreachable))
		for _, a := range unreachable {
			h.EvidenceIDs = append(h.EvidenceIDs, a.ID)
		}
	case timer == TimerInactive:
		h.State = StateFailing
		h.Reason = "the sync timer is installed but not active"
		h.EvidenceIDs = []int64{last.ID}
	case last.Result == store.SyncResultPaused:
		h.State = StatePaused
	}
	return h
}

func orUnknown(s string) string {
	if s == "" {
		return "sync failed"
	}
	return s
}

func roundDuration(d time.Duration) string {
	if d >= time.Hour {
		return fmt.Sprintf("%dh", int(d.Round(time.Hour)/time.Hour))
	}
	return d.Round(time.Minute).String()
}

// ReadThreshold is the unreachable threshold: the human-set
// sync_unreachable_hours setting, else DefaultUnreachableThreshold. A present
// but unusable value is treated as absent.
func ReadThreshold(st *store.Store) time.Duration {
	v, ok, err := st.GetSetting(SettingUnreachableHours)
	if err != nil || !ok {
		return DefaultUnreachableThreshold
	}
	h, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil || h <= 0 {
		return DefaultUnreachableThreshold
	}
	return time.Duration(h * float64(time.Hour))
}

// Check reads the store and applies Health: the entry point for every surface.
func Check(st *store.Store, timer TimerState, now time.Time) (HealthReport, error) {
	peers, err := st.SyncPeers()
	if err != nil {
		return HealthReport{}, err
	}
	var attempts []store.SyncAttempt
	for _, p := range peers {
		if !p.Enabled {
			continue
		}
		a, err := st.SyncAttemptsSinceLastOK(p)
		if err != nil {
			return HealthReport{}, err
		}
		attempts = append(attempts, a...)
	}
	return Health(peers, attempts, timer, now, ReadThreshold(st)), nil
}

// FailingLine is the one line `status` prints first while failing.
func (h PeerHealth) FailingLine() string {
	return fmt.Sprintf("SYNC FAILING with %s: %s (last ok %s)", h.Peer.Label(), h.Reason, h.LastOKText())
}

// BlockLine is the SessionStart warning for a failing peer.
func (h PeerHealth) BlockLine() string {
	since := "never"
	if h.HasOK {
		since = h.LastOKText()
	}
	return fmt.Sprintf("⚠ Backstory sync with %s is failing (%s); work from %s since %s may be missing. Tell the user before resuming.",
		h.Peer.Label(), h.Reason, h.Peer.Label(), since)
}

// NotifyCommand sends one desktop notification. Tests replace it. The default
// is notify-send (Omarchy's mako); a missing notify-send is ignored silently.
var NotifyCommand = func(title, body string) error {
	path, err := exec.LookPath("notify-send")
	if err != nil {
		return nil
	}
	return exec.Command(path, "--app-name=Backstory", title, body).Run() //nolint:gosec // fixed binary from PATH, fixed flags
}

// Notify announces a transition of rep, once: failing after not-failing, and
// ok after failing. The last announced state lives in the local settings
// table, so a timer tick that finds the same state says nothing. A notifier
// error is returned but the state is still recorded (never re-fire per tick).
func Notify(st *store.Store, rep HealthReport) error {
	state, _, err := st.GetSetting(SettingNotifiedState)
	if err != nil {
		return err
	}
	failing := rep.IsFailing()
	var title, body, next string
	switch {
	case failing && state != "failing":
		f := rep.Failing()[0]
		title = "Backstory sync is failing"
		body = fmt.Sprintf("%s: %s (last ok %s)", f.Peer.Label(), f.Reason, f.LastOKText())
		next = "failing"
	case !failing && state == "failing":
		title, body, next = "Backstory sync recovered", "Sync is working again.", "ok"
	default:
		return nil
	}
	if err := st.SetSetting(SettingNotifiedState, next); err != nil {
		return err
	}
	if nerr := NotifyCommand(title, body); nerr != nil && !errors.Is(nerr, exec.ErrNotFound) {
		return nerr
	}
	return nil
}
