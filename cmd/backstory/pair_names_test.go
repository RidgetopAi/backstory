package main

import (
	"os"
	"strings"
	"testing"
)

const (
	testFQDN  = "desk.tail1234.ts.net"
	testShort = "desk"
)

func init() {
	// Never let a developer's real tailscale leak into the pair tests.
	tailnetNames = func() []string { return nil }
}

func withTailnetNames(t *testing.T, names ...string) {
	t.Helper()
	old := tailnetNames
	tailnetNames = func() []string { return names }
	t.Cleanup(func() { tailnetNames = old })
}

// knownOnly serves the client only for the listed hosts and otherwise fails
// the way ssh does for an unknown host key.
func knownOnly(bin string, a syncHome, known ...string) string {
	return `case "$1" in
` + strings.Join(known, "|") + `) ` + serveScript(bin, a) + ` ;;
*) echo "Host key verification failed." >&2; exit 255 ;;
esac`
}

func pairWithNames(t *testing.T, known []string, args ...string) (b syncHome, backLog, out string, code int) {
	t.Helper()
	bin := buildBackstory(t)
	a := newSyncHome(t, "/a/widgets")
	b = newSyncHome(t, "/b/widgets")
	var hosts []string
	for _, k := range known {
		hosts = append(hosts, localUser()+"@"+k)
	}
	forward, backLog := twoWayScripts(t, bin, b, knownOnly(bin, a, hosts...))
	useTransport(t, a, forward)
	code, out, errs := runLinkCLI(t, append([]string{"pair"}, append(args, "laptop")...)...)
	if code != 0 || errs != "" {
		t.Fatalf("pair: code=%d out=%q err=%q", code, out, errs)
	}
	return b, backLog, out, code
}

func TestPairBackPrefersFQDNWhenKnown(t *testing.T) {
	withTailnetNames(t, testFQDN, testShort)
	b, _, out, _ := pairWithNames(t, []string{testFQDN})
	checkPeerHost(t, b, localUser()+"@"+testFQDN)
	if strings.Contains(out, "cannot reach") {
		t.Fatalf("unexpected warning: %q", out)
	}
}

func TestPairBackFallsBackToIPWhenNamesUnknown(t *testing.T) {
	withTailnetNames(t, testFQDN, testShort)
	b, backLog, out, _ := pairWithNames(t, []string{testClientAddr})
	checkPeerHost(t, b, localUser()+"@"+testClientAddr)
	if strings.Contains(out, "cannot reach") {
		t.Fatalf("unexpected warning: %q", out)
	}
	if got := strings.Fields(readFile(t, backLog)); len(got) != 3 {
		t.Fatalf("back checks %v, want FQDN, short, IP", got)
	}
}

func TestPairBackNothingKnownRecordsFQDNAndWarns(t *testing.T) {
	withTailnetNames(t, testFQDN, testShort)
	b, _, out, code := pairWithNames(t, nil)
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	checkPeerHost(t, b, localUser()+"@"+testFQDN)
	if !strings.Contains(out, "cannot reach back") || !strings.Contains(out, "--back <ssh-host>") {
		t.Fatalf("missing --back remedy: %q", out)
	}
}

func TestPairBackNoNamesKeepsSSHClientAddress(t *testing.T) {
	withTailnetNames(t)
	b, backLog, _, _ := pairWithNames(t, []string{testClientAddr})
	checkBackHost(t, b, backLog, localUser()+"@"+testClientAddr)
}

func TestPairExplicitBackSkipsCandidates(t *testing.T) {
	withTailnetNames(t, testFQDN, testShort)
	b, backLog, _, _ := pairWithNames(t, []string{}, "--back", "desk-alias")
	if got := strings.TrimSpace(readFile(t, backLog)); got != "desk-alias" {
		t.Fatalf("back checks %q, want only desk-alias", got)
	}
	checkPeerHost(t, b, "desk-alias")
}

func checkPeerHost(t *testing.T, b syncHome, want string) {
	t.Helper()
	if hosts := peerHosts(t, b); len(hosts) != 1 || hosts[0] != want {
		t.Fatalf("B hosts %v, want [%s]", hosts, want)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	raw, err := os.ReadFile(p) //nolint:gosec // t.TempDir() path
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// B removes A; A pairs B again advertising names where only the short name is
// known. B's row is enabled again and holds the short name, not the removed host.
func TestRePairAfterRemovePicksFreshBackHost(t *testing.T) {
	withTailnetNames(t, testFQDN, testShort)
	bin := buildBackstory(t)
	a := newSyncHome(t, "/a/widgets")
	b := newSyncHome(t, "/b/widgets")
	hOld := localUser() + "@" + testFQDN
	forward, _ := twoWayScripts(t, bin, b, knownOnly(bin, a, hOld))
	useTransport(t, a, forward)
	if code, out, errs := runLinkCLI(t, "pair", "laptop"); code != 0 || errs != "" {
		t.Fatalf("first pair: %d %q %q", code, out, errs)
	}
	checkPeerHost(t, b, hOld)

	if out, errs, code := runCmdIn(t, b.xdg, "pair", "--remove", hOld); code != 0 {
		t.Fatalf("remove on B: %d %q %q", code, out, errs)
	}
	st := b.open(t)
	peers, _ := st.SyncPeers()
	_ = st.Close()
	if len(peers) != 1 || peers[0].Enabled || peers[0].SSHHost != hOld {
		t.Fatalf("after remove %+v, want one disabled row keeping %s", peers, hOld)
	}

	hShort := localUser() + "@" + testShort
	forward, _ = twoWayScripts(t, bin, b, knownOnly(bin, a, hShort))
	useTransport(t, a, forward)
	if code, out, errs := runLinkCLI(t, "pair", "laptop"); code != 0 || errs != "" {
		t.Fatalf("re-pair: %d %q %q", code, out, errs)
	}
	st = b.open(t)
	peers, _ = st.SyncPeers()
	_ = st.Close()
	if len(peers) != 1 || !peers[0].Enabled || peers[0].SSHHost != hShort {
		t.Fatalf("after re-pair %+v, want one enabled row at %s", peers, hShort)
	}
}
