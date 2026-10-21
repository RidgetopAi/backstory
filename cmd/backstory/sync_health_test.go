package main

import (
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/store"
	bsync "github.com/RidgetopAi/backstory/internal/sync"
)

// TestStatusSyncFailingFirstLineAndExit: while a peer's latest attempt is an
// error, `status` prints SYNC FAILING first and exits exitSyncFailing; once
// an ok attempt follows, it exits 0 with no such line.
func TestStatusSyncFailingFirstLineAndExit(t *testing.T) {
	bin := buildBackstory(t)
	dbPath, _, env := startTestDaemon(t, bin)
	st := mustOpenTestStore(t, dbPath)
	if err := st.PairSyncPeer("dev-laptop-0001", "laptop", "laptop.local"); err != nil {
		t.Fatal(err)
	}
	add := func(result, detail string) {
		t.Helper()
		if err := st.AppendSyncAttempt(store.SyncAttempt{Peer: "laptop", DeviceID: "dev-laptop-0001", Result: result, Detail: detail}); err != nil {
			t.Fatal(err)
		}
	}

	add("ok", "")
	if out, code := runStatusSubprocess(t, bin, env); code != 0 || strings.Contains(out, "SYNC FAILING") {
		t.Fatalf("healthy status: exit %d, out %q; want 0 and no SYNC FAILING", code, out)
	}

	add("error", "remote backstory speaks sync proto 2, this one 1")
	out, code := runStatusSubprocess(t, bin, env)
	if code != exitSyncFailing {
		t.Fatalf("failing status exit = %d, want %d\n%s", code, exitSyncFailing, out)
	}
	first := strings.SplitN(out, "\n", 2)[0]
	if !strings.HasPrefix(first, "SYNC FAILING with laptop: remote backstory speaks sync proto 2, this one 1 (last ok ") {
		t.Fatalf("first line = %q", first)
	}

	add("ok", "")
	if out, code := runStatusSubprocess(t, bin, env); code != 0 || strings.Contains(out, "SYNC FAILING") {
		t.Fatalf("recovered status: exit %d, out %q", code, out)
	}
}

// TestQuietSyncNotifiesOnlyOnTransitions: ok, error, error, ok runs of
// `sync --quiet` invoke the notifier exactly twice: failing, then recovery.
func TestQuietSyncNotifiesOnlyOnTransitions(t *testing.T) {
	bin := buildBackstory(t)
	a, b := newSyncHome(t, "/a/widgets"), newSyncHome(t, "/b/widgets")
	useTransport(t, a, serveScript(bin, b))
	if code, out, errs := runLinkCLI(t, "pair", "laptop"); code != 0 {
		t.Fatalf("pair: %d %q %q", code, out, errs)
	}

	var sent []string
	old := bsync.NotifyCommand
	bsync.NotifyCommand = func(title, body string) error { sent = append(sent, title+" | "+body); return nil }
	t.Cleanup(func() { bsync.NotifyCommand = old })
	t.Setenv("HOME", t.TempDir()) // no sync timer installed

	for i, step := range []struct {
		script string
		code   int
	}{
		{serveScript(bin, b), 0},
		{"echo broken >&2; exit 7", 1},
		{"echo broken >&2; exit 7", 1},
		{serveScript(bin, b), 0},
	} {
		useTransport(t, a, step.script)
		if code, out, errs := runLinkCLI(t, "sync", "--quiet"); code != step.code {
			t.Fatalf("run %d: exit %d want %d (out %q err %q)", i, code, step.code, out, errs)
		}
		if i == 1 && len(sent) != 1 {
			t.Fatalf("after the first failure notifications = %v, want 1", sent)
		}
	}
	if len(sent) != 2 || !strings.Contains(sent[0], "failing") || !strings.Contains(sent[1], "recovered") {
		t.Fatalf("notifications = %v, want failing then recovered", sent)
	}
}
