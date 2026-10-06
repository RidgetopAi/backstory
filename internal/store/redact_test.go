package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/testsecrets"
)

func TestRedactionOnInsertAndReadback(t *testing.T) {
	cases := []struct {
		name       string
		text       string
		wantKind   string
		wantAbsent string // the raw secret substring that must never appear
	}{
		{
			name:       "bearer token",
			text:       "used Authorization: Bearer abcdef0123456789.secret-token to call the API",
			wantKind:   "bearer-token",
			wantAbsent: "abcdef0123456789.secret-token",
		},
		{
			name:       "sk- api key",
			text:       "OPENAI_API_KEY=sk-abcdefghijklmnopqrstuvwx",
			wantKind:   "api-key",
			wantAbsent: "sk-abcdefghijklmnopqrstuvwx",
		},
		{
			name:       "aws key",
			text:       "found AKIAABCDEFGHIJKLMNOP in the diff",
			wantKind:   "aws-key",
			wantAbsent: "AKIAABCDEFGHIJKLMNOP",
		},
		{
			name: "pem block",
			text: "-----BEGIN RSA PRIVATE KEY-----\n" +
				"MIIEpAIBAAKCAQEA1c7...redactme...\n" +
				"-----END RSA PRIVATE KEY-----",
			wantKind:   "pem-block",
			wantAbsent: "MIIEpAIBAAKCAQEA1c7",
		},
		{
			name:       "password assignment",
			text:       "config had password=hunter2 in it",
			wantKind:   "password",
			wantAbsent: "hunter2",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
			id, err := s.InsertRecord(InsertRecordParams{
				Identity: Identity{Kind: IdentityAgent},
				Kind:     KindNote,
				Text:     tc.text,
			})
			if err != nil {
				t.Fatalf("InsertRecord: %v", err)
			}

			rec, err := s.GetRecord(id)
			if err != nil {
				t.Fatalf("GetRecord: %v", err)
			}

			if strings.Contains(rec.Text, tc.wantAbsent) {
				t.Errorf("stored text still contains the raw secret: %q", rec.Text)
			}
			wantMarker := "[redacted:" + tc.wantKind + "]"
			if !strings.Contains(rec.Text, wantMarker) {
				t.Errorf("stored text = %q, want it to contain %q", rec.Text, wantMarker)
			}

			// Read back over search too: the raw secret must not resurface there either.
			results, err := s.SearchRecords("redactme OR hunter2 OR abcdefghijklmnopqrstuvwx", 10)
			if err != nil {
				t.Fatalf("SearchRecords: %v", err)
			}
			for _, r := range results {
				if strings.Contains(r.Text, tc.wantAbsent) {
					t.Errorf("SearchRecords returned the raw secret: %q", r.Text)
				}
			}
		})
	}
}

func TestRedactionLeavesControlTextVerbatim(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	const control = "decided to use SQLite with FTS5 compiled in, no cgo required"

	id, err := s.InsertRecord(InsertRecordParams{
		Identity: Identity{Kind: IdentityAgent},
		Kind:     KindNote,
		Text:     control,
	})
	if err != nil {
		t.Fatalf("InsertRecord: %v", err)
	}

	rec, err := s.GetRecord(id)
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if rec.Text != control {
		t.Errorf("control text stored as %q, want unchanged %q", rec.Text, control)
	}
}

// TestToolOutputExcerptRedactsBeforeCutting is task c9ab6d28's DONE WHEN
// clause 3: a PEM private-key block straddling the excerpt's cut point is
// stored as the redaction marker, never as partial key text. Redacting only
// AFTER the cut would leave the block's BEGIN line (or half its body)
// without an END line, which no pattern matches.
func TestToolOutputExcerptRedactsBeforeCutting(t *testing.T) {
	const keyBody = "MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7VJTUt9Us8cKj"
	pem := "-----BEGIN PRIVATE KEY-----\n" + strings.Repeat(keyBody+"\n", 40) + "-----END PRIVATE KEY-----"
	// Place the block so the head budget (~half the cap) ends inside it.
	prefix := strings.Repeat("p", payload.ToolOutputExcerptMaxRunes/2-60)
	out := prefix + pem + strings.Repeat("s", 4000)

	got := ToolOutputExcerpt(out)
	if !strings.Contains(got, "[redacted:pem-block]") {
		t.Errorf("excerpt lacks the pem-block redaction marker: %.200q", got)
	}
	for _, leak := range []string{"BEGIN PRIVATE KEY", keyBody[:20]} {
		if strings.Contains(got, leak) {
			t.Errorf("excerpt leaks partial key text %q", leak)
		}
	}
	if n := utf8.RuneCountInString(got); n > payload.ToolOutputExcerptMaxRunes {
		t.Errorf("excerpt is %d runes, want at most %d", n, payload.ToolOutputExcerptMaxRunes)
	}
}

func TestElideMiddleKeepsHeadTailWithinCap(t *testing.T) {
	in := "HEAD" + strings.Repeat("é", 5000) + "TAIL"
	got := payload.ElideMiddle(in, 100)
	if n := utf8.RuneCountInString(got); n > 100 {
		t.Errorf("got %d runes, want <= 100", n)
	}
	if !strings.HasPrefix(got, "HEAD") || !strings.HasSuffix(got, "TAIL") || !strings.Contains(got, "runes elided") {
		t.Errorf("got %q, want head, tail and marker", got)
	}
	if short := "short"; payload.ElideMiddle(short, 100) != short {
		t.Error("short input must pass through unchanged")
	}
}

// TestRedactFamilyTable: every credential family the store's users hold is
// scrubbed from a note's records.text, and an env-style line keeps its name.
func TestRedactFamilyTable(t *testing.T) {
	for _, f := range testsecrets.All() {
		t.Run(f.Family, func(t *testing.T) {
			got := Redact(f.Text)
			if strings.Contains(got, f.Secret) {
				t.Fatalf("Redact left the secret in place: %q", got)
			}
			if !strings.Contains(got, "[redacted:") {
				t.Fatalf("Redact(%q) = %q, want a marker", f.Text, got)
			}
			if f.Keep != "" && !strings.Contains(got, f.Keep) {
				t.Fatalf("Redact dropped the variable name %q: %q", f.Keep, got)
			}

			st := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
			id, err := st.InsertRecord(InsertRecordParams{Identity: Identity{Kind: IdentityAgent}, Kind: KindNote, Text: f.Text})
			if err != nil {
				t.Fatalf("InsertRecord: %v", err)
			}
			rec, err := st.GetRecord(id)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(rec.Text, f.Secret) || (f.Keep != "" && !strings.Contains(rec.Text, f.Keep)) {
				t.Fatalf("records.text = %q", rec.Text)
			}
			if _, err := st.AppendEvent(Event{TS: time.Now(), Kind: "shell.command", Source: "shell", Payload: f.Text}); err != nil {
				t.Fatalf("AppendEvent: %v", err)
			}
			var stored string
			if err := st.DB().QueryRow(`SELECT payload FROM timeline_events`).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(stored, f.Secret) {
				t.Fatalf("stored payload carries the secret: %q", stored)
			}
		})
	}
}

// TestRedactLeavesProseAlone: ordinary words, shas and uuids are not secrets.
func TestRedactLeavesProseAlone(t *testing.T) {
	for _, in := range []string{
		"the token bucket",
		"set the Authorization header",
		"commit 0123456789abcdef0123456789abcdef01234567 landed",
		"id 123e4567-e89b-12d3-a456-426614174000",
		"export PATH=/usr/bin",
	} {
		if got := Redact(in); got != in {
			t.Errorf("Redact(%q) = %q, want unchanged", in, got)
		}
	}
	got := Redact("bearer Authorization: Bearer X")
	if got != "bearer Authorization: Bearer X" {
		t.Errorf("bearer prose = %q, want unchanged (words bearer and Authorization kept)", got)
	}
	if got := Redact("Authorization: Bearer abcdef0123456789"); strings.Contains(got, "abcdef0123456789") {
		t.Errorf("real bearer token survived: %q", got)
	}
}
