package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/store"
	"github.com/RidgetopAi/backstory/internal/testsecrets"
)

// TestCodexBackfillRedactsCommandAndOutput: a fake sk-ant- value in a Codex
// shell command and its output is absent from every stored payload.
func TestCodexBackfillRedactsCommandAndOutput(t *testing.T) {
	secret := testsecrets.All()[0].Secret
	root := t.TempDir()
	dir := filepath.Join(root, "2026", "01", "15")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	fixture := strings.Join([]string{
		`{"type":"session_meta","timestamp":"2026-01-15T09:00:00Z","ordinal":1,"payload":{"id":"redact-thread-uuid","cwd":"/home/erin/codex-proj","cli_version":"0.151.0","model_provider":"openai","parent_thread_id":"","source":"cli"}}`,
		`{"type":"response_item","timestamp":"2026-01-15T09:00:03Z","ordinal":2,"payload":{"type":"function_call","name":"shell","arguments":"{\"command\":[\"echo\",\"` + secret + `\"]}","call_id":"call_1"}}`,
		`{"type":"response_item","timestamp":"2026-01-15T09:00:04Z","ordinal":3,"payload":{"type":"function_call_output","call_id":"call_1","output":"` + secret + `"}}`,
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "rollout-20260115T090000-redact0thread.jsonl"), []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	st := mustOpenStore(t)
	if _, err := Import(st, Options{Root: root, Git: fakeGit{}, Workspaces: []string{}}); err != nil {
		t.Fatalf("Import: %v", err)
	}
	assertNoSecretStored(t, st, secret)
}

// assertNoSecretStored fails if secret appears in any stored payload, and
// requires at least one tool event so the fixture is known to have imported.
func assertNoSecretStored(t *testing.T, st *store.Store, secret string) {
	t.Helper()
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
