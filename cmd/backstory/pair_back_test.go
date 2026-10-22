package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testClientAddr = "10.9.8.7"

// twoWayScripts wires the transport for a pair run on A: A reaches B through
// the script it is given; B's sync-serve reaches A back through backScript,
// and sees its client at testClientAddr. The back script logs the host it was
// asked for to the returned file.
func twoWayScripts(t *testing.T, bin string, a, b syncHome, backBody string) (forward, backLog string) {
	t.Helper()
	dir := t.TempDir()
	backLog = filepath.Join(dir, "back-hosts")
	back := filepath.Join(dir, "back.sh")
	script := fmt.Sprintf("echo \"$1\" >> %q\n%s\n", backLog, backBody)
	if err := os.WriteFile(back, []byte("#!/bin/sh\n"+script), 0o755); err != nil { //nolint:gosec // test script
		t.Fatal(err)
	}
	forward = fmt.Sprintf("exec env XDG_DATA_HOME=%q SSH_CLIENT=%q BACKSTORY_SYNC_SSH=%q %q sync-serve",
		b.xdg, testClientAddr+" 50000 22", "/bin/sh "+back, bin)
	return forward, backLog
}

func pairedOneWay(t *testing.T, args ...string) (a, b syncHome, bin, ra, rb, backLog string, code int, out, errs string) {
	t.Helper()
	bin = buildBackstory(t)
	a, b = newSyncHome(t, "/a/widgets"), newSyncHome(t, "/b/widgets")
	ra = a.handoff(t, "/a/widgets", "handoff on A")
	rb = b.handoff(t, "/b/widgets", "handoff on B")
	forward, backLog := twoWayScripts(t, bin, a, b, serveScript(bin, a))
	useTransport(t, a, forward)
	code, out, errs = runLinkCLI(t, append([]string{"pair"}, args...)...)
	return a, b, bin, ra, rb, backLog, code, out, errs
}

func TestPairSetsUpBothSidesAndBSyncsBack(t *testing.T) {
	a, b, bin, ra, rb, _, code, out, errs := pairedOneWay(t, "laptop")
	if code != 0 || errs != "" || strings.Contains(out, "cannot reach") {
		t.Fatalf("pair: code=%d out=%q err=%q", code, out, errs)
	}
	sa := a.open(t)
	self, _ := sa.SelfDevice()
	_ = sa.Close()

	sb := b.open(t)
	peers, err := sb.SyncPeers()
	_ = sb.Close()
	if err != nil || len(peers) != 1 || peers[0].DeviceID != self.ID || peers[0].Name != self.Name || !peers[0].Enabled {
		t.Fatalf("B peers %+v err %v; want A %s/%s enabled", peers, err, self.ID, self.Name)
	}

	// sync on B (not A): B reaches A through its own transport.
	useTransport(t, b, serveScript(bin, a))
	code, out, errs = runLinkCLI(t, "sync")
	if code != 0 || !strings.Contains(out, self.Name+": pulled 1, pushed 1") {
		t.Fatalf("sync on B: code=%d out=%q err=%q", code, out, errs)
	}
	if !a.has(t, rb) || !b.has(t, ra) {
		t.Fatal("records did not cross")
	}
	var so, se strings.Builder
	printSyncLines(&so, &se)
	if !strings.HasPrefix(so.String(), "sync: "+self.Name+": last ok 20") {
		t.Fatalf("B status: %q", so.String())
	}

	// B shows A's records by A's name, never the raw id.
	for _, cmd := range []string{"records", "recall"} {
		o, e, c := runCmdIn(t, b.xdg, cmd, "--project", "/b/widgets")
		if c != 0 || !strings.Contains(o, "from "+self.Name) || strings.Contains(o, self.ID[:8]) {
			t.Fatalf("B %s: code=%d out=%q err=%q", cmd, c, o, e)
		}
	}
}

func TestSyncServeNamesClientOnOneSidedPairing(t *testing.T) {
	bin := buildBackstory(t)
	a, b := newSyncHome(t, "/a/widgets"), newSyncHome(t, "/b/widgets")
	a.handoff(t, "/a/widgets", "handoff on A")
	// Pre-change pairing: A knows B, B knows nothing of A.
	sa := a.open(t)
	sb := b.open(t)
	bself, _ := sb.SelfDevice()
	aself, _ := sa.SelfDevice()
	if err := sa.PairSyncPeer(bself.ID, bself.Name, "laptop"); err != nil {
		t.Fatal(err)
	}
	_ = sa.Close()
	_ = sb.Close()
	useTransport(t, a, serveScript(bin, b))
	if code, out, errs := runLinkCLI(t, "sync"); code != 0 {
		t.Fatalf("sync: %d %q %q", code, out, errs)
	}
	for _, cmd := range []string{"records", "recall"} {
		o, e, c := runCmdIn(t, b.xdg, cmd, "--project", "/b/widgets")
		if c != 0 || !strings.Contains(o, "from "+aself.Name) || strings.Contains(o, aself.ID[:8]) {
			t.Fatalf("B %s: code=%d out=%q err=%q", cmd, c, o, e)
		}
	}
}

func TestPairWhenBackPathFailsStillExitsZeroAndBShowsNotOK(t *testing.T) {
	bin := buildBackstory(t)
	a, b := newSyncHome(t, "/a/widgets"), newSyncHome(t, "/b/widgets")
	forward, _ := twoWayScripts(t, bin, a, b, `echo "ssh: connect to host `+testClientAddr+` port 22: Connection refused" >&2; exit 255`)
	useTransport(t, a, forward)
	code, out, errs := runLinkCLI(t, "pair", "laptop")
	if code != 0 || errs != "" {
		t.Fatalf("pair: code=%d out=%q err=%q", code, out, errs)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var warn []string
	for _, l := range lines {
		if strings.Contains(l, "cannot reach back") {
			warn = append(warn, l)
		}
	}
	if len(warn) != 1 || !strings.Contains(warn[0], testClientAddr) || !strings.Contains(warn[0], "Connection refused") || !strings.Contains(warn[0], "backstory pair ") {
		t.Fatalf("pair output %q", out)
	}

	useTransport(t, b, "true")
	var so, se strings.Builder
	printSyncLines(&so, &se)
	if !strings.Contains(so.String(), "last result unreachable") || strings.Contains(so.String(), "last result ok") {
		t.Fatalf("B status %q", so.String())
	}
	st := b.open(t)
	rep, err := syncHealth(st, timeNow())
	_ = st.Close()
	if err != nil || len(rep.Peers) != 1 {
		t.Fatalf("health %+v %v", rep, err)
	}
}

func TestPairBackHostOverrideAndDefault(t *testing.T) {
	_, b, _, _, _, backLog, code, out, errs := pairedOneWay(t, "--back", "desk-alias", "laptop")
	if code != 0 {
		t.Fatalf("pair --back: %d %q %q", code, out, errs)
	}
	checkBackHost(t, b, backLog, "desk-alias")

	_, b, _, _, _, backLog, code, out, errs = pairedOneWay(t, "laptop")
	if code != 0 {
		t.Fatalf("pair: %d %q %q", code, out, errs)
	}
	checkBackHost(t, b, backLog, localUser()+"@"+testClientAddr)
}

func checkBackHost(t *testing.T, b syncHome, backLog, want string) {
	t.Helper()
	st := b.open(t)
	peers, _ := st.SyncPeers()
	_ = st.Close()
	if len(peers) != 1 || peers[0].SSHHost != want {
		t.Fatalf("B peer host %+v, want %q", peers, want)
	}
	if raw, _ := os.ReadFile(backLog); strings.TrimSpace(string(raw)) != want {
		t.Fatalf("back check used %q, want %q", raw, want)
	}
}

// runCmdIn runs a read-only CLI command in-process against dataDir.
func runCmdIn(t *testing.T, dataDir string, args ...string) (string, string, int) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", dataDir)
	code, out, errs := runLinkCLI(t, args...)
	return out, errs, code
}

func timeNow() time.Time { return time.Now() }
