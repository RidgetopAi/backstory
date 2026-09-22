package store

import (
	"path/filepath"
	"strings"
	"testing"
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
