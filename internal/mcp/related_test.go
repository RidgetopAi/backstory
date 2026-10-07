package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

type relatedEnv struct {
	t    *testing.T
	st   *store.Store
	shim *Server
}

func newRelatedEnv(t *testing.T) *relatedEnv {
	st := mustOpenStore(t)
	sock := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	return &relatedEnv{t: t, st: st, shim: dialShim(t, sock)}
}

func (e *relatedEnv) note(args map[string]any) NoteResult {
	e.t.Helper()
	b, _ := json.Marshal(args)
	raw, rerr := e.shim.CallTool(ToolNote, b)
	if rerr != nil {
		e.t.Fatalf("note %s: %v", b, rerr)
	}
	var r NoteResult
	if err := json.Unmarshal(raw, &r); err != nil {
		e.t.Fatalf("unmarshal: %v", err)
	}
	return r
}

func (e *relatedEnv) decision(text string, about ...string) string {
	e.t.Helper()
	m := map[string]any{"kind": "decision", "text": text}
	if about != nil {
		m["about"] = about
	}
	return e.note(m).ID
}

func relatedIDs(r NoteResult) map[string]bool {
	m := map[string]bool{}
	for _, d := range r.RelatedDecisions {
		m[d.ID] = true
	}
	return m
}

func TestRelatedDecisionsByAboutOverlap(t *testing.T) {
	e := newRelatedEnv(t)
	a := e.decision("use Postgres for the store", "db/schema.sql")
	r := e.note(map[string]any{"kind": "decision", "text": "switch the store to SQLite", "about": []string{"db/schema.sql"}})
	if !relatedIDs(r)[a] {
		t.Fatalf("related_decisions = %+v, want %s", r.RelatedDecisions, a)
	}
	if r.Message == "" || !contains(r.Message, "confirm supersede") {
		t.Errorf("Message = %q, want it to name confirm supersede", r.Message)
	}
	if r.RelatedDecisions[0].Text == "" || r.RelatedDecisions[0].TS == "" {
		t.Errorf("candidate lacks text/ts: %+v", r.RelatedDecisions[0])
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func TestRelatedDecisionsByFullText(t *testing.T) {
	e := newRelatedEnv(t)
	a := e.decision("use Postgres for the store")
	e.decision("render the panel with QML")
	r := e.note(map[string]any{"kind": "decision", "text": "drop Postgres, keep a file"})
	ids := relatedIDs(r)
	if !ids[a] || len(ids) != 1 {
		t.Fatalf("related_decisions = %+v, want only %s", r.RelatedDecisions, a)
	}
}

func TestRelatedDecisionsExclusionsAndCap(t *testing.T) {
	e := newRelatedEnv(t)

	// superseded
	sup := e.decision("zebrafish cache layout one")
	e.note(map[string]any{"kind": "decision", "text": "replacement for the first one", "supersedes": sup})
	// tombstoned
	tomb := e.decision("zebrafish cache layout two")
	if err := e.st.TombstoneRecord(tomb, store.Identity{Kind: store.IdentityHuman}); err != nil {
		t.Fatal(err)
	}
	// other project
	if err := e.st.UpsertProject(store.Project{Key: "other", Toplevel: "/o", FirstSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	other, err := e.st.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityHuman}, Kind: store.KindDecision,
		Text: "zebrafish cache layout three", ProjectKey: "other"})
	if err != nil {
		t.Fatal(err)
	}
	// already named in supersedes
	named := e.decision("zebrafish cache layout four")

	r := e.note(map[string]any{"kind": "decision", "text": "zebrafish cache layout five", "supersedes": named})
	ids := relatedIDs(r)
	for name, id := range map[string]string{"superseded": sup, "tombstoned": tomb, "other-project": other, "named-in-supersedes": named} {
		if ids[id] {
			t.Errorf("%s decision %s was returned", name, id)
		}
	}

	// cap: 5 matching current decisions -> at most 3
	e2 := newRelatedEnv(t)
	for i := 0; i < 5; i++ {
		e2.decision(fmt.Sprintf("quokka scheduler variant %d", i))
	}
	r2 := e2.note(map[string]any{"kind": "decision", "text": "quokka scheduler final"})
	if len(r2.RelatedDecisions) != MaxRelatedDecisions {
		t.Errorf("got %d related, want %d", len(r2.RelatedDecisions), MaxRelatedDecisions)
	}
}

func TestRelatedDecisionsOnlyForDecisions(t *testing.T) {
	e := newRelatedEnv(t)
	e.decision("use Postgres for the store", "db/schema.sql")
	for _, kind := range []string{"note", "outcome", "claim", "handoff"} {
		r := e.note(map[string]any{"kind": kind, "text": "Postgres store thoughts", "about": []string{"db/schema.sql"}})
		if r.RelatedDecisions != nil {
			t.Errorf("kind %s: related_decisions = %+v, want none", kind, r.RelatedDecisions)
		}
		if contains(r.Message, "supersede") {
			t.Errorf("kind %s: Message = %q, want no lookup message", kind, r.Message)
		}
	}
}

func TestRelatedDecisionsHostileText(t *testing.T) {
	e := newRelatedEnv(t)
	a := e.decision("use Postgres for the store")
	r := e.note(map[string]any{"kind": "decision", "text": `"quoted" * lone NEAR AND foo: (Postgres) NOT "unterminated`})
	if r.ID == "" {
		t.Fatal("no id")
	}
	if !relatedIDs(r)[a] {
		t.Errorf("related_decisions = %+v, want %s", r.RelatedDecisions, a)
	}
	r = e.note(map[string]any{"kind": "decision", "text": `"*" ( NEAR AND : "`, "about": []string{`we"ird`}})
	if r.ID == "" {
		t.Fatal("no id for operator-only text")
	}
}

func TestRelatedDecisionsWritesNoEdge(t *testing.T) {
	e := newRelatedEnv(t)
	e.decision("use Postgres for the store", "db/schema.sql")
	count := func() int {
		var n int
		if err := e.st.DB().QueryRow(`SELECT COUNT(*) FROM edges`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := count()
	r := e.note(map[string]any{"kind": "decision", "text": "switch the store to SQLite", "about": []string{"db/schema.sql"}})
	if len(r.RelatedDecisions) == 0 {
		t.Fatal("expected related_decisions")
	}
	if got := count(); got != before {
		t.Errorf("edges %d -> %d; the lookup must not write edges", before, got)
	}
}
