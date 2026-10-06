package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/testsecrets"
)

// TestBackfillRedactsEveryCredentialFamily: a transcript carrying each fake
// value in a Bash command and in a tool_result stores neither.
func TestBackfillRedactsEveryCredentialFamily(t *testing.T) {
	for _, f := range testsecrets.All() {
		t.Run(f.Family, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "-home-zed-redact")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			lines := []map[string]any{
				{"type": "user", "uuid": "u1", "sessionId": "sess-redact", "cwd": "/home/zed/redact", "timestamp": "2026-04-01T00:00:01Z",
					"message": map[string]any{"role": "user", "content": "run it"}},
				{"type": "assistant", "uuid": "u2", "sessionId": "sess-redact", "cwd": "/home/zed/redact", "timestamp": "2026-04-01T00:00:02Z",
					"message": map[string]any{"role": "assistant", "content": []any{
						map[string]any{"type": "tool_use", "id": "tu1", "name": "Bash", "input": map[string]any{"command": "echo " + f.Text}}}}},
				{"type": "user", "uuid": "u3", "sessionId": "sess-redact", "cwd": "/home/zed/redact", "timestamp": "2026-04-01T00:00:03Z",
					"message": map[string]any{"role": "user", "content": []any{
						map[string]any{"type": "tool_result", "tool_use_id": "tu1", "content": "out: " + f.Text}}}},
			}
			var b strings.Builder
			for _, l := range lines {
				j, _ := json.Marshal(l)
				b.Write(j)
				b.WriteByte('\n')
			}
			if err := os.WriteFile(filepath.Join(dir, "sess-redact.jsonl"), []byte(b.String()), 0o644); err != nil {
				t.Fatal(err)
			}

			st := mustOpenStore(t)
			if _, err := Import(st, Options{Root: root, Git: fakeGit{}}); err != nil {
				t.Fatalf("Import: %v", err)
			}
			rows, err := st.DB().Query(`SELECT kind, payload FROM timeline_events`)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = rows.Close() }()
			seen := map[string]bool{}
			for rows.Next() {
				var kind, p string
				if err := rows.Scan(&kind, &p); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(p, f.Secret) {
					t.Errorf("%s payload carries the secret: %q", kind, p)
				}
				if (kind == EventToolUse || kind == EventToolResult) && f.Keep != "" && !strings.Contains(p, f.Keep) {
					t.Errorf("%s payload lost the variable name %q: %q", kind, f.Keep, p)
				}
				seen[kind] = true
			}
			if !seen[EventToolUse] || !seen[EventToolResult] {
				t.Fatalf("expected tool.use and tool.result events, saw %v", seen)
			}
		})
	}
}
