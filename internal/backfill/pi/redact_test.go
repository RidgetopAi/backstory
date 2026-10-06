package pi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/store"
	"github.com/RidgetopAi/backstory/internal/testsecrets"
)

// TestPiBackfillRedactsCommandAndOutput: a fake sk-ant- value in a Pi tool
// call's command and its tool result is absent from every stored payload.
func TestPiBackfillRedactsCommandAndOutput(t *testing.T) {
	secret := testsecrets.All()[0].Secret
	root := t.TempDir()
	dir := filepath.Join(root, "--home-bob-redact--")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	fixture := strings.Join([]string{
		`{"id":"root-r","parentId":"","timestamp":"2026-05-02T00:00:00Z","type":"session","cwd":"/home/bob/redact"}`,
		`{"id":"user-r1","parentId":"root-r","timestamp":"2026-05-02T00:00:01Z","type":"message","role":"user","text":"run it"}`,
		`{"id":"asst-r1","parentId":"user-r1","timestamp":"2026-05-02T00:00:02Z","type":"message","role":"assistant","content":[{"type":"toolCall","id":"call-r1","name":"Bash","arguments":{"command":"echo ` + secret + `"}}]}`,
		`{"id":"result-r1","parentId":"asst-r1","timestamp":"2026-05-02T00:00:03Z","type":"toolResult","toolCallId":"call-r1","isError":false,"content":"` + secret + `"}`,
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "2026-05-02T00-00-00Z_root-r.jsonl"), []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	st := mustOpenStore(t)
	if _, err := Import(st, Options{Root: root, Git: fakeGit{}}); err != nil {
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
