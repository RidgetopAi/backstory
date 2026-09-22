package claude

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
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

	// Not truncated or skipped: the huge filler on both sides of the secret
	// survived the round trip through the 64KiB-line-hostile scanner path.
	const fillerRun = 550000
	if got := strings.Count(result.Content, "x"); got < fillerRun {
		t.Errorf("stored content has %d 'x' filler bytes, want at least %d (line was truncated)", got, fillerRun)
	}

	const secret = "sk-abcdefghijklmnopqrstuvwx"
	if strings.Contains(result.Content, secret) {
		t.Errorf("stored tool.result content still contains the raw secret %q", secret)
	}
	if !strings.Contains(result.Content, "[redacted:api-key]") {
		t.Errorf("stored tool.result content = %q (truncated), want it to contain [redacted:api-key]", excerpt(result.Content))
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
