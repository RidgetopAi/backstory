package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// Dismiss is a human-only power: an agent identity writes nothing and the
// event stays undismissed; the human's dismissal is read back for exactly
// that event and session.
func TestDismissGitStateIsHumanOnly(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "backstory.db"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	sid, err := s.StartSession(StartSessionParams{ID: "sess-d", Agent: "claude", CWD: "/home/x/p", StartedAt: time.Now(), Origin: OriginLive})
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.AppendEvent(Event{TS: time.Now(), Kind: "session.git_state", SessionID: sid, Source: "daemon", Payload: `{"uncommitted_count":2}`})
	if err != nil {
		t.Fatal(err)
	}
	ev, found, err := s.GetEvent(id)
	if err != nil || !found || ev.SessionID != sid || ev.Kind != "session.git_state" {
		t.Fatalf("GetEvent(%d) = %+v, %v, %v", id, ev, found, err)
	}
	if _, found, err := s.GetEvent(id + 1000); err != nil || found {
		t.Fatalf("GetEvent(missing) found=%v err=%v, want not found", found, err)
	}

	for _, kind := range []IdentityKind{IdentityAgent, IdentityInference} {
		if err := s.DismissGitState(Identity{Kind: kind, Actor: "agent"}, ev); !errors.Is(err, ErrDismissHumanOnly) {
			t.Fatalf("dismiss by kind %d: err = %v, want ErrDismissHumanOnly", kind, err)
		}
	}
	if d, err := s.GitStateDismissed(ev); err != nil || d {
		t.Fatalf("after refused dismissals: dismissed=%v err=%v, want false", d, err)
	}
	if err := s.DismissGitState(Identity{Kind: IdentityHuman, Actor: "human"}, ev); err != nil {
		t.Fatal(err)
	}
	if d, err := s.GitStateDismissed(ev); err != nil || !d {
		t.Fatalf("after human dismissal: dismissed=%v err=%v, want true", d, err)
	}
}
