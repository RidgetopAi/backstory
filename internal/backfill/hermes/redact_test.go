package hermes

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/testsecrets"
)

// TestHermesBackfillRedactsCommandAndOutput: a fake sk-ant- value in a Hermes
// tool call's command and its tool message is absent from every stored payload.
func TestHermesBackfillRedactsCommandAndOutput(t *testing.T) {
	secret := testsecrets.All()[0].Secret
	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	ended := t0.Add(time.Minute)
	dbPath := filepath.Join(t.TempDir(), "state.db")
	newFixtureDB(t, dbPath,
		[]fixtureSession{{id: "sess-redact", cwd: "/work/repo", gitBranch: "main", gitRepoRoot: "/work/repo", startedAt: t0, endedAt: &ended}},
		[]fixtureMessage{
			{sessionID: "sess-redact", role: "assistant",
				toolCalls: `[{"id":"call_1","name":"Bash","arguments":{"command":"echo ` + secret + `"}}]`,
				ts:        t0.Add(time.Second), active: true},
			{sessionID: "sess-redact", role: "tool", toolCallID: "call_1", content: secret,
				ts: t0.Add(2 * time.Second), active: true},
		})
	st := mustOpenStore(t)
	if _, err := Import(st, Options{Path: dbPath, Git: fakeGit{}, Workspaces: []string{}}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	rows, err := st.DB().Query(`SELECT kind, payload FROM timeline_events`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	tool := 0
	for rows.Next() {
		var kind, p string
		if err := rows.Scan(&kind, &p); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(p, secret) {
			t.Errorf("%s payload carries the secret: %q", kind, p)
		}
		if strings.HasPrefix(kind, "tool.") {
			tool++
		}
	}
	if tool == 0 {
		t.Fatal("no tool events imported; fixture did not exercise the redactor")
	}
}
