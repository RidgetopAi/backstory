package store

import (
	"path/filepath"
	"testing"
	"time"
)

// TestLastActivityIgnoresActivityAfterAsOf: This Week's "last activity" is
// as of the view's own clock; a record written after asOf must not appear.
func TestLastActivityIgnoresActivityAfterAsOf(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "backstory.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	if err := s.UpsertProject(Project{Key: "p", Toplevel: "/p", FirstSeen: base}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	if _, err := s.StartSession(StartSessionParams{Agent: "claude", CWD: "/p", ProjectKey: "p", StartedAt: base, Origin: OriginLive}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if _, err := s.StartSession(StartSessionParams{Agent: "claude", CWD: "/p", ProjectKey: "p", StartedAt: base.Add(48 * time.Hour), Origin: OriginLive}); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	got, ok, err := s.LastActivity("p", base.Add(24*time.Hour))
	if err != nil || !ok {
		t.Fatalf("LastActivity: ok=%v err=%v", ok, err)
	}
	if !got.Equal(base) {
		t.Fatalf("LastActivity as of base+24h = %s, want %s (the later session is after asOf)", got, base)
	}
}
