package main

import (
	"strings"
	"testing"
)

// With a paired peer, delete and purge print the also-forgotten line, and
// status counts pending forgets until a sync pushes them.
func TestForgetPrintsAlsoForgottenAndStatusCountsPendingForgets(t *testing.T) {
	bin := buildBackstory(t)
	a, b := newSyncHome(t, "/a/widgets"), newSyncHome(t, "/b/widgets")
	r1 := a.handoff(t, "/a/widgets", "handoff one")
	r2 := a.handoff(t, "/a/widgets", "handoff two")

	var out, errb strings.Builder
	// Unpaired: no extra line.
	t.Setenv("XDG_DATA_HOME", a.xdg)
	if code := runDelete([]string{r1, "--yes"}, strings.NewReader(""), &out, &errb); code != 0 || strings.Contains(out.String(), "also be forgotten") {
		t.Fatalf("unpaired delete: code=%d out=%q err=%q", code, out.String(), errb.String())
	}

	useTransport(t, a, serveScript(bin, b))
	if code, o, e := runLinkCLI(t, "pair", "laptop"); code != 0 {
		t.Fatalf("pair: %d %q %q", code, o, e)
	}
	if code, o, e := runLinkCLI(t, "sync"); code != 0 {
		t.Fatalf("sync: %d %q %q", code, o, e)
	}
	st := a.open(t)
	peers, _ := st.SyncPeers()
	_ = st.Close()
	name := peers[0].Name

	out.Reset()
	errb.Reset()
	if code := runDelete([]string{r2, "--yes"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("delete: code=%d err=%q", code, errb.String())
	}
	if want := "will also be forgotten on " + name + " at next sync\n"; !strings.Contains(out.String(), want) {
		t.Fatalf("delete output %q lacks %q", out.String(), want)
	}
	out.Reset()
	if code := runPurge([]string{"--project", "/a/widgets", "--yes"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("purge: code=%d err=%q", code, errb.String())
	}
	if !strings.Contains(out.String(), "will also be forgotten on "+name+" at next sync") {
		t.Fatalf("purge output %q", out.String())
	}

	var so, se strings.Builder
	printSyncLines(&so, &se)
	if want := "sync: 1 forget(s) waiting to reach " + name; !strings.Contains(so.String(), want) {
		t.Fatalf("status %q lacks %q", so.String(), want)
	}
	if code, o, e := runLinkCLI(t, "sync"); code != 0 {
		t.Fatalf("sync: %d %q %q", code, o, e)
	}
	so.Reset()
	printSyncLines(&so, &se)
	if strings.Contains(so.String(), "waiting to reach") {
		t.Fatalf("status after sync still pending: %q", so.String())
	}
}
