package sync_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
	bsync "github.com/RidgetopAi/backstory/internal/sync"
)

// helperDBEnv makes this test binary act as `backstory sync-serve` for the
// store at that path: the local subprocess a test transport runs as the peer.
const helperDBEnv = "BACKSTORY_SYNC_TEST_SERVE_DB"

const helperBatchEnv = "BACKSTORY_SYNC_TEST_SERVE_BATCH"

func TestMain(m *testing.M) {
	if db := os.Getenv(helperDBEnv); db != "" {
		st, err := store.Open(db, nil, nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		batch, _ := strconv.Atoi(os.Getenv(helperBatchEnv))
		err = bsync.Serve(st, human, os.Stdin, os.Stdout, bsync.Options{BatchSize: batch})
		_ = st.Close()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type device struct {
	st   *store.Store
	path string
}

func newDevice(t *testing.T, project string) device {
	t.Helper()
	p := filepath.Join(t.TempDir(), "b.db")
	s, err := store.Open(p, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	addProject(t, s, project, remoteURL)
	return device{st: s, path: p}
}

func script(t *testing.T, body string) bsync.Transport {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ssh.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil { //nolint:gosec // test script
		t.Fatal(err)
	}
	return bsync.Transport{Command: []string{"/bin/sh", p}}
}

// serveTransport runs the peer as a local subprocess serving d's store.
func serveTransport(t *testing.T, d device) bsync.Transport {
	t.Helper()
	return serveBatched(t, d, 0)
}

// serveBatched is serveTransport with the peer serving batch feed entries per pull.
func serveBatched(t *testing.T, d device, batch int) bsync.Transport {
	t.Helper()
	return script(t, fmt.Sprintf("exec env %s=%q %s=%d %q", helperDBEnv, d.path, helperBatchEnv, batch, os.Args[0]))
}

func syncer(d device, tr bsync.Transport) *bsync.Syncer {
	return &bsync.Syncer{Store: d.st, Identity: human, Transport: tr}
}

func handoff(t *testing.T, s *store.Store, project, text string) string {
	t.Helper()
	rid, err := s.InsertRecordWithEdges(store.InsertRecordParams{Identity: human, Kind: store.KindHandoff, Text: text, Next: "next", ProjectKey: project}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return rid
}

func onlyPeer(t *testing.T, s *store.Store) store.SyncPeerRow {
	t.Helper()
	ps, err := s.SyncPeers()
	if err != nil || len(ps) != 1 {
		t.Fatalf("peers=%v err=%v", ps, err)
	}
	return ps[0]
}

func attempts(t *testing.T, s *store.Store) int {
	return count(t, s, `SELECT COUNT(*) FROM sync_attempts`)
}

func TestPairThenSyncMovesBothWaysAndSecondSyncMovesNothing(t *testing.T) {
	a, b := newDevice(t, "/a/widgets"), newDevice(t, "/b/widgets")
	ra := handoff(t, a.st, "/a/widgets", "handoff written on A")
	rb := handoff(t, b.st, "/b/widgets", "handoff written on B")

	y := syncer(a, serveTransport(t, b))
	info, err := y.Pair("laptop")
	if err != nil {
		t.Fatal(err)
	}
	bDev, _ := b.st.SelfDevice()
	if info.DeviceID != bDev.ID || info.DeviceName != bDev.Name {
		t.Fatalf("pair info %+v, want device %+v", info, bDev)
	}
	p := onlyPeer(t, a.st)
	if !p.Enabled || p.SSHHost != "laptop" || p.DeviceID != bDev.ID {
		t.Fatalf("peer %+v", p)
	}

	o := y.SyncPeer(p)
	if !o.OK() || o.Pulled != 1 || o.Pushed != 1 {
		t.Fatalf("first sync %+v", o)
	}
	if _, err := a.st.GetRecord(rb); err != nil {
		t.Fatalf("B's handoff missing on A: %v", err)
	}
	if _, err := b.st.GetRecord(ra); err != nil {
		t.Fatalf("A's handoff missing on B: %v", err)
	}

	p = onlyPeer(t, a.st)
	if p.PullCursor == 0 || p.PushCursor == 0 {
		t.Fatalf("cursors not persisted: %+v", p)
	}
	o = y.SyncPeer(p)
	if !o.OK() || o.Pulled != 0 || o.Pushed != 0 {
		t.Fatalf("second sync moved something: %+v", o)
	}

	// Dry run counts both ways and changes nothing.
	handoff(t, a.st, "/a/widgets", "another from A")
	handoff(t, b.st, "/b/widgets", "another from B")
	p = onlyPeer(t, a.st)
	recA, recB, rows := count(t, a.st, `SELECT COUNT(*) FROM records`), count(t, b.st, `SELECT COUNT(*) FROM records`), attempts(t, a.st)
	dry := syncer(a, serveTransport(t, b))
	dry.Options.DryRun = true
	o = dry.SyncPeer(p)
	if !o.OK() || o.WouldPull != 1 || o.WouldPush != 1 || o.Pulled != 0 || o.Pushed != 0 {
		t.Fatalf("dry run %+v", o)
	}
	if got := onlyPeer(t, a.st); got != p {
		t.Fatalf("dry run changed the peer: %+v -> %+v", p, got)
	}
	if count(t, a.st, `SELECT COUNT(*) FROM records`) != recA || count(t, b.st, `SELECT COUNT(*) FROM records`) != recB || attempts(t, a.st) != rows {
		t.Fatal("dry run changed a store or the attempts log")
	}
	o = y.SyncPeer(p)
	if !o.OK() || o.Pulled != 1 || o.Pushed != 1 {
		t.Fatalf("real sync after dry run %+v", o)
	}
}

func TestFailuresAreClassifiedAndEachLogsOneAttempt(t *testing.T) {
	a, b := newDevice(t, "/a/widgets"), newDevice(t, "/b/widgets")
	if _, err := syncer(a, serveTransport(t, b)).Pair("laptop"); err != nil {
		t.Fatal(err)
	}
	p := onlyPeer(t, a.st)

	cases := []struct {
		name, script, result, detail string
	}{
		{"connection refused", `echo "ssh: connect to host laptop port 22: Connection refused" >&2; exit 255`, "unreachable", "Connection refused"},
		{"connect timeout", `echo "ssh: connect to host laptop port 22: Connection timed out" >&2; exit 255`, "unreachable", "timed out"},
		{"dns", `echo "ssh: Could not resolve hostname laptop: Name or service not known" >&2; exit 255`, "unreachable", "resolve"},
		{"auth refused", `echo "laptop: Permission denied (publickey)." >&2; exit 255`, "error", "Permission denied"},
		{"no sync-serve", `echo 'backstory: unknown command "sync-serve"' >&2; exit 2`, "error", "proto 0 vs 1"},
		{"no backstory", `echo "bash: backstory: command not found" >&2; exit 127`, "error", "no backstory"},
		{"other non-zero", `echo boom >&2; exit 3`, "error", "exited 3"},
		{"proto mismatch", `echo '{"type":"hello","proto":0,"device_id":"` + p.DeviceID + `","device_name":"old"}'; cat >/dev/null`, "error", "proto 0 vs 1"},
		{"another device at the host", `echo '{"type":"hello","proto":1,"device_id":"someone-else","device_name":"imposter"}'; cat >/dev/null`, "error", "not the paired device"},
	}
	for _, c := range cases {
		before := attempts(t, a.st)
		o := syncer(a, script(t, c.script)).SyncPeer(p)
		if o.Result != c.result || !strings.Contains(o.Detail, c.detail) {
			t.Errorf("%s: got %s %q, want %s containing %q", c.name, o.Result, o.Detail, c.result, c.detail)
		}
		if got := attempts(t, a.st); got != before+1 {
			t.Errorf("%s: attempts %d -> %d, want one new row", c.name, before, got)
		}
		last, err := a.st.SyncAttempts(p.Label(), 1)
		if err != nil || len(last) != 1 || last[0].Result != c.result || last[0].Detail != o.Detail {
			t.Errorf("%s: logged %+v err=%v", c.name, last, err)
		}
	}

	// Capture off here is paused, whatever the peer says.
	if err := a.st.BeginCapturePause(time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	before := attempts(t, a.st)
	o := syncer(a, serveTransport(t, b)).SyncPeer(p)
	if o.Result != store.SyncResultPaused || o.OK() {
		t.Fatalf("capture off: %+v", o)
	}
	if attempts(t, a.st) != before+1 {
		t.Fatal("paused sync did not log one attempt")
	}
}

func TestImportFailurePartwayKeepsCursorAndNextSyncImportsOnce(t *testing.T) {
	a, b := newDevice(t, "/a/widgets"), newDevice(t, "/b/widgets")
	if _, err := syncer(a, serveTransport(t, b)).Pair("laptop"); err != nil {
		t.Fatal(err)
	}
	first := handoff(t, b.st, "/b/widgets", "first handoff")
	second := handoff(t, b.st, "/b/widgets", "POISON second handoff")
	if _, err := a.st.DB().Exec(`CREATE TRIGGER poison BEFORE INSERT ON records WHEN new.text LIKE '%POISON%'
		BEGIN SELECT RAISE(ABORT, 'poisoned'); END`); err != nil {
		t.Fatal(err)
	}
	y := syncer(a, serveBatched(t, b, 1))

	o := y.SyncPeer(onlyPeer(t, a.st))
	if o.Result != store.SyncResultError || !strings.Contains(o.Detail, "poisoned") {
		t.Fatalf("failing import: %+v", o)

	}
	p := onlyPeer(t, a.st)
	if _, err := a.st.GetRecord(first); err != nil {
		t.Fatalf("first batch should have committed: %v", err)
	}
	if _, err := a.st.GetRecord(second); err == nil {
		t.Fatal("poisoned record was imported")
	}
	bMax, _ := b.st.SyncMaxSeq()
	if p.PullCursor <= 0 || p.PullCursor >= bMax {
		t.Fatalf("pull cursor %d must sit after the committed batch only (peer feed ends at %d)", p.PullCursor, bMax)
	}
	pulled := o.Pulled

	if _, err := a.st.DB().Exec(`DROP TRIGGER poison`); err != nil {
		t.Fatal(err)
	}
	o = y.SyncPeer(p)
	if !o.OK() || o.Pulled != 1 {
		t.Fatalf("retry must import only the failed batch, got %+v (first run pulled %d)", o, pulled)
	}
	if n := count(t, a.st, `SELECT COUNT(*) FROM records WHERE id IN (?, ?)`, first, second); n != 2 {
		t.Fatalf("records after retry: %d", n)
	}
}

func TestFirstBatchFailureLeavesCursorAtZero(t *testing.T) {
	a, b := newDevice(t, "/a/widgets"), newDevice(t, "/b/widgets")
	if _, err := syncer(a, serveTransport(t, b)).Pair("laptop"); err != nil {
		t.Fatal(err)
	}
	second := handoff(t, b.st, "/b/widgets", "POISON only handoff")
	if _, err := a.st.DB().Exec(`CREATE TRIGGER poison BEFORE INSERT ON records WHEN new.text LIKE '%POISON%'
		BEGIN SELECT RAISE(ABORT, 'poisoned'); END`); err != nil {
		t.Fatal(err)
	}
	y := syncer(a, serveTransport(t, b))
	if o := y.SyncPeer(onlyPeer(t, a.st)); o.OK() {
		t.Fatalf("expected failure: %+v", o)
	}
	if p := onlyPeer(t, a.st); p.PullCursor != 0 {
		t.Fatalf("cursor advanced to %d though nothing committed", p.PullCursor)
	}
	if _, err := a.st.DB().Exec(`DROP TRIGGER poison`); err != nil {
		t.Fatal(err)
	}
	if o := y.SyncPeer(onlyPeer(t, a.st)); !o.OK() || o.Pulled != 1 {
		t.Fatalf("retry %+v", o)
	}
	if _, err := a.st.GetRecord(second); err != nil {
		t.Fatal(err)
	}
}

func TestPairRefusesSelfAndAttemptsAreAppendOnly(t *testing.T) {
	a := newDevice(t, "/a/widgets")
	if _, err := syncer(a, serveTransport(t, a)).Pair("me"); err == nil || !strings.Contains(err.Error(), "itself") {
		t.Fatalf("self pair: %v", err)
	}
	if ps, _ := a.st.SyncPeers(); len(ps) != 0 {
		t.Fatalf("self was recorded as a peer: %v", ps)
	}
	if _, err := syncer(a, script(t, `echo '{"type":"hello","proto":0,"device_id":"x","device_name":"old"}'; cat >/dev/null`)).Pair("old"); err == nil || !strings.Contains(err.Error(), "proto 0 vs 1") {
		t.Fatalf("proto pair: %v", err)
	}
	if err := a.st.AppendSyncAttempt(store.SyncAttempt{Peer: "x", Result: store.SyncResultOK}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.st.DB().Exec(`UPDATE sync_attempts SET detail = 'x'`); err == nil {
		t.Fatal("sync_attempts accepted an UPDATE")
	}
	if _, err := a.st.DB().Exec(`DELETE FROM sync_attempts`); err == nil {
		t.Fatal("sync_attempts accepted a DELETE")
	}
	if err := a.st.AppendSyncAttempt(store.SyncAttempt{Peer: "x", Result: "bogus"}); err == nil {
		t.Fatal("sync_attempts accepted an unknown result")
	}
}
