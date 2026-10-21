package sync_test

import (
	"bytes"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
	bsync "github.com/RidgetopAi/backstory/internal/sync"
)

const remoteURL = "git@github.com:acme/widgets.git"

var (
	human = store.Identity{Kind: store.IdentityHuman, Actor: "human"}
	agent = store.Identity{Kind: store.IdentityAgent, Actor: "sess"}
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "b.db"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func addProject(t *testing.T, s *store.Store, key, remote string) {
	t.Helper()
	if err := s.UpsertProject(store.Project{Key: key, RemoteURL: remote, Toplevel: key, FirstSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

func note(t *testing.T, s *store.Store, id store.Identity, project, session, text string, edges ...store.EdgeSpec) string {
	t.Helper()
	rid, err := s.InsertRecordWithEdges(store.InsertRecordParams{
		Identity: id, Kind: store.KindDecision, Text: text, ProjectKey: project, SessionID: session,
	}, edges)
	if err != nil {
		t.Fatal(err)
	}
	return rid
}

// link runs one sync with `client` pulling from and pushing to `server`
// across in-process pipes. It returns the report, the client's error, and
// every byte the client received from the server.
func link(t *testing.T, client *store.Store, clientID store.Identity, server *store.Store, serverID store.Identity, from bsync.Cursors) (bsync.Report, []byte, error) {
	t.Helper()
	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	var received bytes.Buffer
	done := make(chan error, 1)
	go func() {
		err := bsync.Serve(server, serverID, c2sR, s2cW, bsync.Options{})
		_ = s2cW.Close()
		_ = c2sR.Close()
		done <- err
	}()
	rep, err := bsync.Run(client, clientID, from, io.TeeReader(s2cR, &received), c2sW, bsync.Options{})
	_ = c2sW.Close()
	_, _ = io.Copy(io.Discard, s2cR)
	<-done
	return rep, received.Bytes(), err
}

func count(t *testing.T, s *store.Store, q string, args ...any) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestReplicationIsIdempotentAndKeepsTierAndOrigin(t *testing.T) {
	a, b := openStore(t), openStore(t)
	addProject(t, a, "/a/widgets", remoteURL)
	addProject(t, b, "/b/widgets", remoteURL)
	sess, err := a.StartSession(store.StartSessionParams{Agent: "claude", CWD: "/secret/cwd", ProjectKey: "/a/widgets",
		StartedAt: time.Now(), Origin: store.OriginLive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.AppendEvent(store.Event{TS: time.Now(), Kind: "tool.use", SessionID: sess, Source: "daemon", Payload: `{"name":"RAW-TIMELINE-MARK"}`}); err != nil {
		t.Fatal(err)
	}
	r1 := note(t, a, agent, "/a/widgets", sess, "agent decided X")
	r2 := note(t, a, human, "/a/widgets", sess, "human decided Y",
		store.EdgeSpec{OtherID: r1, Type: store.EdgeSupersedes, DeclaredBy: "human", Field: "supersedes"})
	if err := a.EndSession(sess, time.Now(), "exit"); err != nil {
		t.Fatal(err)
	}
	eventsB := count(t, b, `SELECT COUNT(*) FROM timeline_events`)

	rep, wire, err := link(t, b, human, a, human, bsync.Cursors{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pulled.Records != 2 || rep.Pulled.Edges != 1 || rep.Pulled.Sessions != 1 {
		t.Fatalf("first run pulled %+v", rep.Pulled)
	}
	aDev, _ := a.SelfDevice()
	for id, tier := range map[string]store.Tier{r1: store.TierAgentDeclared, r2: store.TierHumanDeclared} {
		got, err := b.GetRecord(id)
		if err != nil {
			t.Fatalf("record %s missing on B: %v", id, err)
		}
		if got.Tier != tier || got.ProjectKey != "/b/widgets" || got.SessionID != sess {
			t.Fatalf("record %s on B: tier=%s project=%s session=%s", id, got.Tier, got.ProjectKey, got.SessionID)
		}
		var origin string
		if err := b.DB().QueryRow(`SELECT origin_device FROM records WHERE id = ?`, id).Scan(&origin); err != nil || origin != aDev.ID {
			t.Fatalf("origin_device=%q err=%v, want A %q", origin, err, aDev.ID)
		}
	}
	if n := count(t, b, `SELECT COUNT(*) FROM edges WHERE from_id = ? AND to_id = ? AND type = 'supersedes'`, r2, r1); n != 1 {
		t.Fatalf("edge not replicated: %d", n)
	}
	var origin, cwd, sessOrigin string
	var ended *int64
	if err := b.DB().QueryRow(`SELECT origin, cwd, ended_at, origin_device FROM sessions WHERE id = ?`, sess).Scan(&origin, &cwd, &ended, &sessOrigin); err != nil {
		t.Fatal(err)
	}
	if origin != "synced" || cwd != "" || ended == nil || sessOrigin != aDev.ID {
		t.Fatalf("session stub origin=%s cwd=%q ended=%v", origin, cwd, ended)
	}
	if bytes.Contains(wire, []byte("RAW-TIMELINE-MARK")) || bytes.Contains(wire, []byte("/secret/cwd")) {
		t.Fatal("raw timeline or cwd crossed the wire")
	}
	if got := count(t, b, `SELECT COUNT(*) FROM timeline_events`); got != eventsB {
		t.Fatalf("B timeline_events changed: %d -> %d", eventsB, got)
	}

	// Second run, from zero and from the cursors: nothing imported either way.
	for _, from := range []bsync.Cursors{{}, rep.Cursors} {
		rep2, _, err := link(t, b, human, a, human, from)
		if err != nil {
			t.Fatal(err)
		}
		if rep2.Pulled.Imported() != 0 || rep2.Pushed.Imported() != 0 {
			t.Fatalf("second run imported pulled=%+v pushed=%+v", rep2.Pulled, rep2.Pushed)
		}
	}
	if got := count(t, b, `SELECT COUNT(*) FROM records`); got != 2 {
		t.Fatalf("B has %d records", got)
	}
}

func TestTombstonesCrossWithNoText(t *testing.T) {
	a, b := openStore(t), openStore(t)
	addProject(t, a, "/a/widgets", remoteURL)
	addProject(t, b, "/b/widgets", remoteURL)

	synced := note(t, a, human, "/a/widgets", "", "uniqueword-synced then forgotten")
	first, _, err := link(t, b, human, a, human, bsync.Cursors{})
	if err != nil {
		t.Fatal(err)
	}
	if hits, _ := b.SearchRecords("uniqueword", 10); len(hits) != 1 {
		t.Fatalf("expected the record searchable on B before the tombstone, got %d", len(hits))
	}
	if err := a.TombstoneRecord(synced, human); err != nil {
		t.Fatal(err)
	}
	// Resume from the cursors: the tombstone must be its own change.
	rep, _, err := link(t, b, human, a, human, first.Cursors)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Pulled.Tombstones != 1 {
		t.Fatalf("tombstone after sync did not cross: %+v", rep.Pulled)
	}
	got, _ := b.GetRecord(synced)
	if got.Text != "" || got.TombstonedAt == nil {
		t.Fatalf("B record not scrubbed: %+v", got)
	}
	if hits, _ := b.SearchRecords("uniqueword", 10); len(hits) != 0 {
		t.Fatal("tombstoned text still searchable on B")
	}
	if n := count(t, b, `SELECT COUNT(*) FROM records_fts WHERE records_fts MATCH 'uniqueword'`); n != 0 {
		t.Fatalf("FTS index on B still holds the text: %d", n)
	}

	// Tombstoned before the first sync: once through the normal scrub, once as
	// a pre-scrub tombstone whose row still holds its text.
	b2 := openStore(t)
	addProject(t, b2, "/b/widgets", remoteURL)
	normal := note(t, a, human, "/a/widgets", "", "SECRET-NORMAL-text")
	legacy := note(t, a, human, "/a/widgets", "", "SECRET-LEGACY-text")
	if err := a.TombstoneRecord(normal, human); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB().Exec(`UPDATE records SET tombstoned_at = ? WHERE id = ?`, time.Now().UnixNano(), legacy); err != nil {
		t.Fatal(err)
	}
	if n := count(t, a, `SELECT COUNT(*) FROM records WHERE id = ? AND text <> ''`, legacy); n != 1 {
		t.Fatal("test setup: the legacy tombstone should still hold its text on A")
	}
	_, wire, err := link(t, b2, human, a, human, bsync.Cursors{})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(wire, []byte("SECRET-")) {
		t.Fatalf("tombstoned text crossed the wire: %s", wire)
	}
	for _, id := range []string{normal, legacy} {
		r, err := b2.GetRecord(id)
		if err != nil || r.Text != "" || r.TombstonedAt == nil {
			t.Fatalf("record %s on B: %+v err=%v", id, r, err)
		}
	}
}

func TestProjectsWithoutIdentityAreNotServedAndPlaceholdersRehome(t *testing.T) {
	a, b := openStore(t), openStore(t)
	addProject(t, a, "/a/widgets", remoteURL)
	addProject(t, a, "/a/loose", "") // no remote, no link: no sync identity
	loose := note(t, a, human, "/a/loose", "", "never leaves A")
	onB := note(t, a, human, "/a/widgets", "", "for a project B lacks")

	changes, _, _, err := a.ChangesSince(0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range changes {
		if c.Record != nil && c.Record.ID == loose {
			t.Fatal("record of a project with no sync identity was served")
		}
	}
	rep, wire, err := link(t, b, human, a, human, bsync.Cursors{})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(wire, []byte("never leaves A")) {
		t.Fatal("identity-less record crossed the wire")
	}
	if _, err := b.GetRecord(loose); err == nil {
		t.Fatal("B has the identity-less record")
	}
	const placeholder = "sync:github.com/acme/widgets"
	got, err := b.GetRecord(onB)
	if err != nil || got.ProjectKey != placeholder {
		t.Fatalf("record should land under %s: %+v err=%v", placeholder, got, err)
	}

	// B gains a local project with that identity: the placeholder rehomes.
	addProject(t, b, "/b/widgets", remoteURL)
	got, _ = b.GetRecord(onB)
	if got.ProjectKey != "/b/widgets" {
		t.Fatalf("record not rehomed: project %s", got.ProjectKey)
	}
	if n := count(t, b, `SELECT COUNT(*) FROM projects WHERE key = ?`, placeholder); n != 0 {
		t.Fatal("placeholder project survives")
	}
	if n := count(t, b, `SELECT COUNT(*) FROM project_key_aliases WHERE legacy_key = ? AND canonical_key = '/b/widgets'`, placeholder); n != 1 {
		t.Fatal("rehome did not record the alias")
	}

	// A project that gains an identity later has its earlier records served.
	if err := a.SetProjectLink(human, "loose-notes", "/a/loose"); err != nil {
		t.Fatal(err)
	}
	rep2, _, err := link(t, b, human, a, human, rep.Cursors)
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Pulled.Records != 1 {
		t.Fatalf("record of a newly identified project not served: %+v", rep2.Pulled)
	}
	if got, err := b.GetRecord(loose); err != nil || got.ProjectKey != "sync:loose-notes" {
		t.Fatalf("loose record on B: %+v %v", got, err)
	}
}

func TestProtoMismatchIsRefusedAndNothingImported(t *testing.T) {
	a, b := openStore(t), openStore(t)
	addProject(t, a, "/a/widgets", remoteURL)
	note(t, a, human, "/a/widgets", "", "x")

	// Server refuses a client speaking proto 2.
	var out bytes.Buffer
	in := strings.NewReader(`{"type":"hello","proto":2,"device_id":"d","device_name":"n"}` + "\n" +
		`{"type":"push","changes":[{"kind":"record","record":{"id":"r","kind":"note","tier":"human-declared","text":"t","project_sync_id":"github.com/acme/widgets","ts_ns":1,"origin_device":"d"}}]}` + "\n")
	err := bsync.Serve(b, human, in, &out, bsync.Options{})
	var pm *bsync.ProtoMismatchError
	if !errors.As(err, &pm) || pm.Local != 1 || pm.Remote != 2 {
		t.Fatalf("server error %v, want ProtoMismatchError{1,2}", err)
	}
	if !strings.Contains(out.String(), "proto_mismatch") {
		t.Fatalf("server did not answer with the typed error: %s", out.String())
	}
	if n := count(t, b, `SELECT COUNT(*) FROM records`); n != 0 {
		t.Fatal("server imported after refusing")
	}

	// Client refuses a server speaking proto 2, even one that offers changes.
	server := strings.NewReader(`{"type":"hello","proto":2,"device_id":"d","device_name":"n"}` + "\n" +
		`{"type":"changes","changes":[],"next":0}` + "\n")
	_, err = bsync.Run(b, human, bsync.Cursors{}, server, io.Discard, bsync.Options{})
	if !errors.As(err, &pm) || pm.Local != 1 || pm.Remote != 2 {
		t.Fatalf("client error %v, want ProtoMismatchError{1,2}", err)
	}
	if n := count(t, b, `SELECT COUNT(*) FROM records`); n != 0 {
		t.Fatal("client imported after refusing")
	}
}

func TestCaptureOffPausesImportAndWritesNothing(t *testing.T) {
	a, b := openStore(t), openStore(t)
	addProject(t, a, "/a/widgets", remoteURL)
	note(t, a, human, "/a/widgets", "", "x")
	if err := b.BeginCapturePause(time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	rep, _, err := link(t, b, human, a, human, bsync.Cursors{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Paused || rep.Pulled.Imported() != 0 || rep.Cursors.Pull != 0 {
		t.Fatalf("report %+v", rep)
	}
	res, err := b.ImportSyncChanges(human, store.SyncPeer{DeviceID: "d"}, []store.SyncChange{{Kind: store.SyncKindSession,
		Session: &store.SyncSession{ID: "s", Agent: "x", StartedAt: 1}}})
	if err != nil || !res.Paused {
		t.Fatalf("import result %+v err=%v, want paused", res, err)
	}
	for _, tbl := range []string{"records", "sessions", "projects", "edges"} {
		if n := count(t, b, `SELECT COUNT(*) FROM `+tbl); n != 0 {
			t.Fatalf("paused import wrote %d %s", n, tbl)
		}
	}
	// The peer being paused is reported, not an error.
	a2 := openStore(t)
	if err := a2.BeginCapturePause(time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	rep, _, err = link(t, b, human, a2, human, bsync.Cursors{})
	_ = rep
	if err != nil {
		t.Fatal(err)
	}
}

func TestImportRefusesAgentIdentity(t *testing.T) {
	a, b := openStore(t), openStore(t)
	addProject(t, a, "/a/widgets", remoteURL)
	r := note(t, a, human, "/a/widgets", "", "x")
	changes, _, _, err := a.ChangesSince(0, 100)
	if err != nil || len(changes) == 0 {
		t.Fatalf("changes=%d err=%v", len(changes), err)
	}
	for _, id := range []store.Identity{agent, {Kind: store.IdentityInference, Actor: "daemon"}} {
		if _, err := b.ImportSyncChanges(id, store.SyncPeer{DeviceID: "d"}, changes); !errors.Is(err, store.ErrSyncImportRequiresHuman) {
			t.Fatalf("identity %v: err=%v", id.Kind, err)
		}
	}
	if _, err := b.GetRecord(r); err == nil {
		t.Fatal("refused import still wrote")
	}
	// A server run as an agent refuses pushes.
	_, _, err = link(t, a, human, b, agent, bsync.Cursors{})
	var re *bsync.RemoteError
	if !errors.As(err, &re) || re.Code != bsync.CodeImportRefused {
		t.Fatalf("err=%v, want import_refused", err)
	}
	if n := count(t, b, `SELECT COUNT(*) FROM records`); n != 0 {
		t.Fatal("agent-run server imported")
	}
}

func TestTierComesFromTheChangeNotTheImporter(t *testing.T) {
	b := openStore(t)
	addProject(t, b, "/b/widgets", remoteURL)
	ch := []store.SyncChange{{Kind: store.SyncKindRecord, Record: &store.SyncRecord{
		ID: "r1", TS: 5, Kind: "note", Tier: "inferred", Text: "t", ProjectSyncID: "github.com/acme/widgets", OriginDevice: "dev-a"}}}
	if _, err := b.ImportSyncChanges(human, store.SyncPeer{DeviceID: "dev-a"}, ch); err != nil {
		t.Fatal(err)
	}
	got, _ := b.GetRecord("r1")
	if got.Tier != store.TierInferred {
		t.Fatalf("tier %s, want inferred", got.Tier)
	}
}
