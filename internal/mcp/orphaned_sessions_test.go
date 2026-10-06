package mcp

import (
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/block"
	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/store"
)

// TestDaemonStartEndsSessionsLeftLiveByPreviousRun is task 6d68ac6f's DONE
// WHEN clause 1: a store holding six live rows for one alive pid (previous
// daemon runs) renders that pid at most once in a new reader's Coordination
// slot after the start-up sweep, and the previous rows have ended_at set.
//
// RA-MUTATION-PROBE: skip EndOrphanedSessions -> RED (rows stay live and
// the live-session list is not empty); drop the pid collapse -> the
// pre-sweep render lists the pid six times.
func TestDaemonStartEndsSessionsLeftLiveByPreviousRun(t *testing.T) {
	st := mustOpenStore(t)
	const key, pid = "wobble-party", 16873
	if err := st.UpsertProject(store.Project{Key: key, Toplevel: key, FirstSeen: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	var old []string
	for i := 0; i < 6; i++ {
		p := pid
		id, err := st.StartSession(store.StartSessionParams{
			Agent: "claude", CWD: "/home/brian/wobble-party", ProjectKey: key, PID: &p,
			StartedAt: time.Now().Add(-time.Duration(i+1) * time.Hour), Origin: store.OriginLive,
		})
		if err != nil {
			t.Fatalf("StartSession: %v", err)
		}
		old = append(old, id)
	}
	// A backfilled row is not a daemon's to end.
	imp, err := st.StartSession(store.StartSessionParams{
		Agent: "claude", CWD: "/x", ProjectKey: key, StartedAt: time.Now(), Origin: store.OriginBackfilled,
	})
	if err != nil {
		t.Fatalf("StartSession backfill: %v", err)
	}

	procfs := fakeProcFS{status: map[int]ident.Status{pid: {Name: "claude"}, 4242: {Name: "claude"}}}
	render := func(reader string) string {
		out, err := block.Render(block.Params{
			Store: st, ProcFS: procfs, ProjectKey: key, SessionID: reader, Harness: "claude", Now: time.Now(),
		})
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		return out
	}

	now := time.Now()
	n, err := EndOrphanedSessions(st, now)
	if err != nil || n != 6 {
		t.Fatalf("EndOrphanedSessions = %d, %v; want 6, nil", n, err)
	}
	readerPID := 4242
	reader, err := st.StartSession(store.StartSessionParams{
		Agent: "claude", CWD: "/home/brian/wobble-party", ProjectKey: key, PID: &readerPID,
		StartedAt: now, Origin: store.OriginLive,
	})
	if err != nil {
		t.Fatalf("StartSession reader: %v", err)
	}

	out := render(reader)
	if c := strings.Count(out, "- claude in /home/brian/wobble-party"); c > 1 {
		t.Errorf("pid listed %d times; got:\n%s", c, out)
	}
	live, err := st.LiveSessionsInProject(key)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range live {
		if s.ID != reader {
			t.Errorf("session %s still live after the sweep", s.ID)
		}
	}
	for _, id := range old {
		var ended *int64
		var kind *string
		if err := st.DB().QueryRow(`SELECT ended_at, exit_kind FROM sessions WHERE id = ?`, id).Scan(&ended, &kind); err != nil {
			t.Fatal(err)
		}
		if ended == nil || kind == nil || *kind != ReasonDaemonRestarted {
			t.Errorf("session %s ended_at=%v exit_kind=%v", id, ended, kind)
		}
	}
	var ended *int64
	if err := st.DB().QueryRow(`SELECT ended_at FROM sessions WHERE id = ?`, imp).Scan(&ended); err != nil || ended != nil {
		t.Errorf("backfilled session was ended: %v %v", ended, err)
	}
}
