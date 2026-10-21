package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// syncHome is one device's data dir for the pair/sync CLI tests.
type syncHome struct{ xdg string }

func newSyncHome(t *testing.T, projectKey string) syncHome {
	t.Helper()
	h := syncHome{xdg: t.TempDir()}
	st := h.open(t)
	if err := st.UpsertProject(store.Project{Key: projectKey, RemoteURL: "git@github.com:acme/widgets.git", Toplevel: projectKey, FirstSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	return h
}

func (h syncHome) open(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(h.xdg, "backstory", "backstory.db"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func (h syncHome) handoff(t *testing.T, projectKey, text string) string {
	t.Helper()
	st := h.open(t)
	defer func() { _ = st.Close() }()
	id, err := st.InsertRecordWithEdges(store.InsertRecordParams{Identity: humanIdentity, Kind: store.KindHandoff, Text: text, Next: "n", ProjectKey: projectKey}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (h syncHome) has(t *testing.T, id string) bool {
	st := h.open(t)
	defer func() { _ = st.Close() }()
	_, err := st.GetRecord(id)
	return err == nil
}

// useTransport points BACKSTORY_SYNC_SSH at a script, and runs the CLI as the
// device whose data dir is local.
func useTransport(t *testing.T, local syncHome, body string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ssh.sh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil { //nolint:gosec // test script
		t.Fatal(err)
	}
	t.Setenv(sshEnvForTest, "/bin/sh "+p)
	t.Setenv("XDG_DATA_HOME", local.xdg)
}

const sshEnvForTest = "BACKSTORY_SYNC_SSH"

func serveScript(bin string, peer syncHome) string {
	return fmt.Sprintf("exec env XDG_DATA_HOME=%q %q sync-serve", peer.xdg, bin)
}

func TestPairSyncStatusAndRemove(t *testing.T) {
	bin := buildBackstory(t)
	a, b := newSyncHome(t, "/a/widgets"), newSyncHome(t, "/b/widgets")
	ra := a.handoff(t, "/a/widgets", "handoff on A")
	rb := b.handoff(t, "/b/widgets", "handoff on B")

	// Pairing yourself is refused, with one plain line, and records nothing.
	useTransport(t, a, serveScript(bin, a))
	code, out, errs := runLinkCLI(t, "pair", "me")
	if code == 0 || !strings.Contains(errs, "itself") || strings.Count(strings.TrimSpace(errs), "\n") != 0 {
		t.Fatalf("self pair: code=%d out=%q err=%q", code, out, errs)
	}
	if n := countSyncPeers(t, a); n != 0 {
		t.Fatalf("self was paired: %d peers", n)
	}

	useTransport(t, a, serveScript(bin, b))
	code, out, errs = runLinkCLI(t, "pair", "laptop")
	if code != 0 || !strings.Contains(out, "Paired with") {
		t.Fatalf("pair: code=%d out=%q err=%q", code, out, errs)
	}
	st := a.open(t)
	peers, _ := st.SyncPeers()
	_ = st.Close()
	if len(peers) != 1 || peers[0].SSHHost != "laptop" || !peers[0].Enabled {
		t.Fatalf("peers %+v", peers)
	}
	name := peers[0].Name

	var so, se strings.Builder
	printSyncLines(&so, &se)
	if want := "sync: " + name + ": last ok never, last result no attempt yet\n"; so.String() != want {
		t.Fatalf("status before sync: %q want %q", so.String(), want)
	}

	if code, out, errs = runLinkCLI(t, "sync", "--dry-run"); code != 0 || !strings.Contains(out, "would pull 1, would push 1") {
		t.Fatalf("dry run: code=%d out=%q err=%q", code, out, errs)
	}
	if a.has(t, rb) || b.has(t, ra) {
		t.Fatal("dry run moved a record")
	}
	if code, out, errs = runLinkCLI(t, "sync"); code != 0 || !strings.Contains(out, "pulled 1, pushed 1") {
		t.Fatalf("sync: code=%d out=%q err=%q", code, out, errs)
	}
	if !a.has(t, rb) || !b.has(t, ra) {
		t.Fatal("handoffs did not cross")
	}
	if code, out, _ = runLinkCLI(t, "sync"); code != 0 || !strings.Contains(out, "pulled 0, pushed 0") {
		t.Fatalf("second sync: code=%d out=%q", code, out)
	}
	so.Reset()
	printSyncLines(&so, &se)
	if !strings.HasPrefix(so.String(), "sync: "+name+": last ok 20") || !strings.Contains(so.String(), "last result ok") {
		t.Fatalf("status after sync: %q", so.String())
	}

	// A failing peer: one plain line naming peer, result and reason; exit 1.
	useTransport(t, a, `echo "ssh: connect to host laptop port 22: Connection refused" >&2; exit 255`)
	code, out, errs = runLinkCLI(t, "sync")
	if code == 0 || strings.TrimSpace(errs) != "sync: "+name+" unreachable: ssh: connect to host laptop port 22: Connection refused" {
		t.Fatalf("unreachable sync: code=%d out=%q err=%q", code, out, errs)
	}
	so.Reset()
	printSyncLines(&so, &se)
	if !strings.Contains(so.String(), "last result unreachable") || !strings.Contains(so.String(), "last ok 20") {
		t.Fatalf("status after failure: %q", so.String())
	}

	// --remove disables the peer and keeps what it sent.
	if code, out, errs = runLinkCLI(t, "pair", "--remove", name); code != 0 {
		t.Fatalf("remove: code=%d out=%q err=%q", code, out, errs)
	}
	st = a.open(t)
	peers, _ = st.SyncPeers()
	_ = st.Close()
	if len(peers) != 1 || peers[0].Enabled {
		t.Fatalf("peer after remove: %+v", peers)
	}
	if !a.has(t, rb) {
		t.Fatal("remove deleted the peer's records")
	}
	if code, out, _ = runLinkCLI(t, "sync"); code != 0 || !strings.Contains(out, "No paired devices") {
		t.Fatalf("sync after remove: code=%d out=%q", code, out)
	}
	if code, _, errs = runLinkCLI(t, "sync", name); code == 0 || !strings.Contains(errs, "was removed") {
		t.Fatalf("named sync after remove: code=%d err=%q", code, errs)
	}
	so.Reset()
	printSyncLines(&so, &se)
	if !strings.Contains(so.String(), "removed") {
		t.Fatalf("status after remove: %q", so.String())
	}
}

func countSyncPeers(t *testing.T, h syncHome) int {
	st := h.open(t)
	defer func() { _ = st.Close() }()
	ps, err := st.SyncPeers()
	if err != nil {
		t.Fatal(err)
	}
	return len(ps)
}

func TestSyncNamesEveryFailingPeerAndReason(t *testing.T) {
	bin := buildBackstory(t)
	a, b := newSyncHome(t, "/a/widgets"), newSyncHome(t, "/b/widgets")
	useTransport(t, a, serveScript(bin, b))
	if code, _, errs := runLinkCLI(t, "pair", "laptop"); code != 0 {
		t.Fatal(errs)
	}
	st := a.open(t)
	peers, _ := st.SyncPeers()
	_ = st.Close()
	name := peers[0].Name

	for _, c := range []struct{ body, want string }{
		{`echo 'backstory: unknown command "sync-serve"' >&2; exit 2`, "sync: " + name + " error: remote backstory has no sync-serve, so it has no sync (proto 0 vs 1)"},
		{`echo "laptop: Permission denied (publickey)." >&2; exit 255`, "sync: " + name + " error: laptop: Permission denied (publickey)."},
	} {
		useTransport(t, a, c.body)
		code, _, errs := runLinkCLI(t, "sync")
		if code == 0 || strings.TrimSpace(errs) != c.want {
			t.Errorf("code=%d err=%q want %q", code, errs, c.want)
		}
	}

	// Capture off here: paused, non-zero, named.
	st = a.open(t)
	if err := st.BeginCapturePause(time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	useTransport(t, a, serveScript(bin, b))
	code, _, errs := runLinkCLI(t, "sync")
	if code == 0 || !strings.HasPrefix(errs, "sync: "+name+" paused: ") {
		t.Fatalf("paused sync: code=%d err=%q", code, errs)
	}
}
