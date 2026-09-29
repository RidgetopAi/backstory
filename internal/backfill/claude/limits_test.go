package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/store"
)

// TestImportsOversizedLineAndRedactsSecretPayload is the punch's clause 3:
// a 1MiB tool_result line — far past bufio.Scanner's default 64KiB token
// limit — is imported whole, and a fake API key embedded in it comes back
// out of the store redacted, proven by reading the stored event back
// (store's redaction rule, exercised end-to-end through AppendEvent).
func TestImportsOversizedLineAndRedactsSecretPayload(t *testing.T) {
	st := mustOpenStore(t)
	root := filepath.Join("testdata", "limits", "projects")

	res, err := Import(st, Options{Root: root, Git: fakeGit{}})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if res.SessionsCreated != 1 {
		t.Fatalf("SessionsCreated = %d, want 1", res.SessionsCreated)
	}

	sess := sessionByHarnessID(t, st, "sess-f-uuid")

	rows, err := st.DB().Query(`SELECT kind, payload FROM timeline_events
		WHERE session_id = ? ORDER BY id ASC`, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()

	var resultPayload string
	found := false
	for rows.Next() {
		var kind, payload string
		if err := rows.Scan(&kind, &payload); err != nil {
			t.Fatal(err)
		}
		if kind == EventToolResult {
			resultPayload = payload
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("no tool.result event found for the oversized line")
	}

	var result struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(resultPayload), &result); err != nil {
		t.Fatalf("unmarshal tool.result payload: %v", err)
	}

	// The 1MiB line was imported whole by the scanner, and the stored
	// content is the bounded excerpt (task c9ab6d28): head and tail kept.
	if got := utf8.RuneCountInString(result.Content); got > payload.ToolOutputExcerptMaxRunes {
		t.Errorf("stored content has %d runes, want at most %d", got, payload.ToolOutputExcerptMaxRunes)
	}
	if !strings.Contains(result.Content, "runes elided") {
		t.Errorf("stored content lacks the elision marker: %q", excerpt(result.Content))
	}

	const secret = "sk-abcdefghijklmnopqrstuvwx"
	if strings.Contains(result.Content, secret) {
		t.Errorf("stored tool.result content still contains the raw secret %q", secret)
	}
}

// excerpt keeps a failing assertion's error message readable even though
// the string under test is over a megabyte.
func excerpt(s string) string {
	const max = 200
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated for test output)"
}

func toolUseCount(t *testing.T, st *store.Store, sessionID string) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM timeline_events WHERE session_id = ? AND kind = ?`, sessionID, EventToolUse).Scan(&n); err != nil {
		t.Fatalf("count tool.use events: %v", err)
	}
	return n
}

// TestPartialTailNotImportedUntilComplete is the punch's clause 4's PARTIAL
// TAIL requirement: a transcript whose final bytes are an incomplete JSON
// line (no trailing newline, mid-object — the harness is still flushing
// it) must import every complete line before it, but must not advance the
// cursor past the partial tail. Once the line is completed (the missing
// bytes are appended), a rerun imports exactly that line and nothing is
// lost or duplicated.
func TestPartialTailNotImportedUntilComplete(t *testing.T) {
	st := mustOpenStore(t)
	root := copyDir(t, filepath.Join("testdata", "partial", "projects"))
	path := filepath.Join(root, "-home-quinn-partial", "sess-q.jsonl")

	res1, err := Import(st, Options{Root: root, Git: fakeGit{}})
	if err != nil {
		t.Fatalf("Import (first run): %v", err)
	}
	if res1.SessionsCreated != 1 {
		t.Fatalf("SessionsCreated = %d, want 1", res1.SessionsCreated)
	}
	if res1.LinesSkipped != 0 {
		t.Errorf("LinesSkipped = %d, want 0 (the partial tail must be held back, not counted as a malformed line)", res1.LinesSkipped)
	}

	sess := sessionByHarnessID(t, st, "sess-q-uuid")
	if got := toolUseCount(t, st, sess.ID); got != 0 {
		t.Fatalf("tool.use count after first run = %d, want 0 (the tool_use line is still an incomplete partial tail)", got)
	}

	// Rerun with nothing appended: the partial tail must not have
	// advanced the cursor, so this must still import nothing new.
	res1b, err := Import(st, Options{Root: root, Git: fakeGit{}})
	if err != nil {
		t.Fatalf("Import (rerun, tail still incomplete): %v", err)
	}
	if res1b.SessionsCreated != 0 || res1b.EventsCreated != 0 {
		t.Fatalf("rerun before completion = %+v, want zero sessions/events (must retry the same partial bytes, not skip them)", res1b)
	}

	// Complete the line: append the missing suffix plus the trailing
	// newline. Every byte written before the cursor's saved offset is
	// untouched.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // path is this test's own tempdir copy
	if err != nil {
		t.Fatalf("open %s for append: %v", path, err)
	}
	if _, err := f.WriteString(` -la"}}]}}` + "\n"); err != nil {
		t.Fatalf("append completion bytes: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	res2, err := Import(st, Options{Root: root, Git: fakeGit{}})
	if err != nil {
		t.Fatalf("Import (rerun, completed): %v", err)
	}
	if res2.SessionsCreated != 0 {
		t.Errorf("SessionsCreated on completion rerun = %d, want 0 (existing session, not a new one)", res2.SessionsCreated)
	}
	if got := toolUseCount(t, st, sess.ID); got != 1 {
		t.Errorf("tool.use count after completion rerun = %d, want 1 (0 -> 1, exactly the completed line)", got)
	}
}
