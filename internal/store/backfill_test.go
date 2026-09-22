package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBackfillCursorRoundTrip(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))

	if _, ok, err := s.GetBackfillCursor("claude", "/x.jsonl"); err != nil {
		t.Fatalf("GetBackfillCursor on empty store: %v", err)
	} else if ok {
		t.Fatal("GetBackfillCursor on empty store: ok = true, want false")
	}

	sessionID, err := s.StartSession(StartSessionParams{
		Agent:  "claude",
		CWD:    "/home/a/proj",
		Origin: OriginBackfilled,
	})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	want := BackfillCursor{SessionID: sessionID, LastUUID: "u1", ByteOffset: 512}
	if err := s.SetBackfillCursor("claude", "/x.jsonl", want); err != nil {
		t.Fatalf("SetBackfillCursor: %v", err)
	}
	got, ok, err := s.GetBackfillCursor("claude", "/x.jsonl")
	if err != nil {
		t.Fatalf("GetBackfillCursor: %v", err)
	}
	if !ok {
		t.Fatal("GetBackfillCursor after Set: ok = false, want true")
	}
	if got != want {
		t.Fatalf("GetBackfillCursor = %+v, want %+v", got, want)
	}

	// A second Set for the same source+path updates in place, not a new row.
	want2 := BackfillCursor{SessionID: sessionID, LastUUID: "u2", ByteOffset: 1024}
	if err := s.SetBackfillCursor("claude", "/x.jsonl", want2); err != nil {
		t.Fatalf("SetBackfillCursor (update): %v", err)
	}
	got2, _, err := s.GetBackfillCursor("claude", "/x.jsonl")
	if err != nil {
		t.Fatalf("GetBackfillCursor (update): %v", err)
	}
	if got2 != want2 {
		t.Fatalf("GetBackfillCursor after update = %+v, want %+v", got2, want2)
	}
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM backfill_cursors`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("backfill_cursors row count = %d, want 1 (upsert, not insert)", n)
	}
}

func TestAppendEventRedactsPayload(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))

	const secret = "sk-abcdefghijklmnopqrstuvwx"
	id, err := s.AppendEvent(Event{
		Kind:    "tool.result",
		Source:  "backfill",
		Payload: `{"content":"OPENAI_API_KEY=` + secret + `"}`,
	})
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	var payload string
	if err := s.DB().QueryRow(`SELECT payload FROM timeline_events WHERE id = ?`, id).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if want := "[redacted:api-key]"; !strings.Contains(payload, want) {
		t.Errorf("stored payload = %q, want it to contain %q", payload, want)
	}
	if strings.Contains(payload, secret) {
		t.Errorf("stored payload still contains the raw secret: %q", payload)
	}
}
