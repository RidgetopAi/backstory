package week_test

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/block"
	"github.com/RidgetopAi/backstory/internal/store"
	bsync "github.com/RidgetopAi/backstory/internal/sync"
	"github.com/RidgetopAi/backstory/internal/week"
)

const (
	syncedRemote = "git@github.com:acme/widgets.git"
)

var syncedHuman = store.Identity{Kind: store.IdentityHuman, Actor: "human"}

// machine is one fixture store with the shared repo under its own key and an
// active session in the week window.
type machine struct {
	st   *store.Store
	key  string
	sess string
}

func newMachine(t *testing.T, name string) machine {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "b.db"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	key := "/" + name + "/widgets"
	if err := st.UpsertProject(store.Project{Key: key, RemoteURL: syncedRemote, Toplevel: key, FirstSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	sess, err := st.StartSession(store.StartSessionParams{ID: "sess-" + name, Agent: "claude", CWD: key, ProjectKey: key, StartedAt: time.Now(), Origin: store.OriginLive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendEvent(store.Event{TS: time.Now(), Kind: "tool.use", SessionID: sess, Source: "shell", Payload: `{}`}); err != nil {
		t.Fatal(err)
	}
	return machine{st: st, key: key, sess: sess}
}

func (m machine) handoff(t *testing.T, text string) string {
	t.Helper()
	id, err := m.st.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent}, Kind: store.KindHandoff, Text: text,
		SessionID: m.sess, ProjectKey: m.key,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// pull runs one sync with client pulling from server over in-process pipes.
func pull(t *testing.T, client, server *store.Store) {
	t.Helper()
	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := bsync.Serve(server, syncedHuman, c2sR, s2cW, bsync.Options{})
		_ = s2cW.Close()
		_ = c2sR.Close()
		done <- err
	}()
	_, err := bsync.Run(client, syncedHuman, bsync.Cursors{}, s2cR, c2sW, bsync.Options{})
	_ = c2sW.Close()
	_, _ = io.Copy(io.Discard, s2cR)
	if serr := <-done; err == nil {
		err = serr
	}
	if err != nil {
		t.Fatal(err)
	}
}

func weekHandoff(t *testing.T, m machine) week.ProjectSummary {
	t.Helper()
	res, err := week.Build(week.Params{Store: m.st, Now: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res.WhereLeftOff {
		if r.Project.ProjectKey == m.key {
			return r.Project
		}
	}
	t.Fatalf("no this-week row for %s: %+v", m.key, res.WhereLeftOff)
	return week.ProjectSummary{}
}

// B writes H1 first, A writes H2 later. After a sync the older H1 is inserted
// last on A (and H2 last on B); this-week and SessionStart's Resume must still
// agree on the newest-by-ts handoff on both machines (F12).
func TestThisWeekHandoffMatchesResumeAfterSync(t *testing.T) {
	a, b := newMachine(t, "a"), newMachine(t, "b")
	h1 := b.handoff(t, "H1 older handoff from B")
	time.Sleep(10 * time.Millisecond)
	h2 := a.handoff(t, "H2 newer handoff from A")

	pull(t, a.st, b.st) // H1 arrives on A after H2 (higher rowid, older ts)
	pull(t, b.st, a.st) // H2 arrives on B last

	for name, m := range map[string]machine{"A": a, "B": b} {
		if got := weekHandoff(t, m).HandoffID; got != h2 {
			t.Errorf("%s: this-week handoff = %s, want H2 %s", name, got, h2)
		}
		out, err := block.Render(block.Params{Store: m.st, ProjectKey: m.key, SessionID: m.sess, Harness: "claude", Now: time.Now().Add(time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "H2 newer handoff") {
			t.Errorf("%s: Resume lacks H2:\n%s", name, out)
		}
		if name == "A" && !strings.Contains(out, "also:") {
			t.Errorf("A: Resume lacks the also line for H1 (%s):\n%s", h1, out)
		}
	}
}
