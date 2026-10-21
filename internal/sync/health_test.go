package sync

import (
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

func TestHealthTable(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	th := 24 * time.Hour
	peer := store.SyncPeerRow{DeviceID: "dev-b", Name: "laptop", Enabled: true}
	at := func(id int64, ago time.Duration, result, detail string) store.SyncAttempt {
		return store.SyncAttempt{ID: id, TS: now.Add(-ago), Peer: "laptop", DeviceID: "dev-b", Result: result, Detail: detail}
	}
	cases := []struct {
		name     string
		peers    []store.SyncPeerRow
		attempts []store.SyncAttempt
		timer    TimerState
		on       bool
		want     PeerState // of the first peer
		evidence []int64
	}{
		{"last error", []store.SyncPeerRow{peer}, []store.SyncAttempt{at(1, 3*time.Hour, "ok", ""), at(2, time.Hour, "error", "proto 2 vs 1")}, TimerActive, true, StateFailing, []int64{2}},
		{"error then ok recovers", []store.SyncPeerRow{peer}, []store.SyncAttempt{at(1, 3*time.Hour, "error", "x"), at(2, time.Hour, "ok", "")}, TimerActive, true, StateOK, nil},
		{"unreachable past threshold", []store.SyncPeerRow{peer}, []store.SyncAttempt{at(1, 30*time.Hour, "ok", ""), at(2, 28*time.Hour, "unreachable", "x"), at(3, time.Hour, "unreachable", "x")}, TimerActive, true, StateFailing, []int64{2, 3}},
		{"unreachable under threshold", []store.SyncPeerRow{peer}, []store.SyncAttempt{at(1, 30*time.Hour, "ok", ""), at(2, 10*time.Hour, "unreachable", "x"), at(3, time.Hour, "unreachable", "x")}, TimerActive, true, StateOK, nil},
		{"one old unreachable is not a span", []store.SyncPeerRow{peer}, []store.SyncAttempt{at(1, 50*time.Hour, "unreachable", "x")}, TimerActive, true, StateOK, nil},
		{"unreachable before an ok does not count", []store.SyncPeerRow{peer}, []store.SyncAttempt{at(1, 60*time.Hour, "unreachable", "x"), at(2, 30*time.Hour, "unreachable", "x"), at(3, time.Hour, "ok", "")}, TimerActive, true, StateOK, nil},
		{"timer installed but inactive", []store.SyncPeerRow{peer}, []store.SyncAttempt{at(1, time.Hour, "ok", "")}, TimerInactive, true, StateFailing, []int64{1}},
		{"timer absent is fine", []store.SyncPeerRow{peer}, []store.SyncAttempt{at(1, time.Hour, "ok", "")}, TimerAbsent, true, StateOK, nil},
		{"paused", []store.SyncPeerRow{peer}, []store.SyncAttempt{at(1, time.Hour, "paused", "capture off")}, TimerActive, true, StatePaused, nil},
		{"ok", []store.SyncPeerRow{peer}, []store.SyncAttempt{at(1, time.Hour, "ok", "")}, TimerActive, true, StateOK, nil},
		{"never attempted", []store.SyncPeerRow{peer}, nil, TimerInactive, true, StateNeverSynced, nil},
		{"no enabled peer", []store.SyncPeerRow{{DeviceID: "dev-b", Name: "laptop"}}, []store.SyncAttempt{at(1, time.Hour, "error", "x")}, TimerInactive, false, "", nil},
		{"no peers", nil, nil, TimerActive, false, "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rep := Health(c.peers, c.attempts, c.timer, now, th)
			if rep.On != c.on {
				t.Fatalf("On = %v, want %v", rep.On, c.on)
			}
			if !c.on {
				if len(rep.Peers) != 0 || rep.IsFailing() {
					t.Fatalf("off report has peers/failing: %+v", rep)
				}
				return
			}
			got := rep.Peers[0]
			if got.State != c.want {
				t.Fatalf("State = %q (%s), want %q", got.State, got.Reason, c.want)
			}
			if got.Failing() != (c.want == StateFailing) || rep.IsFailing() != got.Failing() {
				t.Fatalf("Failing mismatch for %q", got.State)
			}
			if c.want == StateFailing && got.Reason == "" {
				t.Fatal("failing without a reason")
			}
			if len(got.EvidenceIDs) != len(c.evidence) {
				t.Fatalf("evidence = %v, want %v", got.EvidenceIDs, c.evidence)
			}
			for i, id := range c.evidence {
				if got.EvidenceIDs[i] != id {
					t.Fatalf("evidence = %v, want %v", got.EvidenceIDs, c.evidence)
				}
			}
		})
	}
}

// TestNotifyOnlyOnTransitions: ok, error, error, ok notifies twice.
func TestNotifyOnlyOnTransitions(t *testing.T) {
	st, err := store.Open(t.TempDir()+"/b.db", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	var sent []string
	old := NotifyCommand
	NotifyCommand = func(title, _ string) error { sent = append(sent, title); return nil }
	defer func() { NotifyCommand = old }()

	failing := HealthReport{On: true, Peers: []PeerHealth{{Peer: store.SyncPeerRow{Name: "laptop"}, State: StateFailing, Reason: "x"}}}
	ok := HealthReport{On: true, Peers: []PeerHealth{{Peer: store.SyncPeerRow{Name: "laptop"}, State: StateOK}}}
	for _, r := range []HealthReport{ok, failing, failing, ok, ok} {
		if err := Notify(st, r); err != nil {
			t.Fatal(err)
		}
	}
	if len(sent) != 2 || sent[0] != "Backstory sync is failing" || sent[1] != "Backstory sync recovered" {
		t.Fatalf("notifications = %v, want failing then recovered", sent)
	}
}
