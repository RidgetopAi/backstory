package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestCapturePauseWindows(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "s.db"))
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := func(h int) time.Time { return t0.Add(time.Duration(h) * time.Hour) }

	if p, _ := s.CapturePausedAt(at(1)); p {
		t.Fatal("paused with no windows")
	}
	if err := s.BeginCapturePause(at(1)); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginCapturePause(at(2)); err != nil { // idempotent while open
		t.Fatal(err)
	}
	if p, _ := s.CapturePausedAt(at(100)); !p {
		t.Error("open window must cover every later instant")
	}
	if err := s.EndCapturePause(at(3)); err != nil {
		t.Fatal(err)
	}
	ps, _ := s.CapturePauses()
	if len(ps) != 1 {
		t.Fatalf("windows = %+v, want 1", ps)
	}
	for h, want := range map[int]bool{0: false, 1: true, 2: true, 3: false, 4: false} {
		if got, _ := s.CapturePausedAt(at(h)); got != want {
			t.Errorf("CapturePausedAt(+%dh) = %v, want %v", h, got, want)
		}
	}
}
