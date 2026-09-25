package mcp

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/socket"
	"github.com/RidgetopAi/backstory/internal/store"
)

// fakeProcFS is a fully synthetic /proc tree keyed on the dialing test
// process's own real pid (SO_PEERCRED can't be forged without root, so the
// connecting side is genuinely this test binary; only what the tree says
// about that pid's ancestry is fake) — the same pattern internal/socket's
// own tests use.
type fakeProcFS struct {
	status map[int]ident.Status
	cwd    map[int]string
}

func (f fakeProcFS) Status(pid int) (ident.Status, error) {
	st, ok := f.status[pid]
	if !ok {
		return ident.Status{}, fmt.Errorf("fakeProcFS: no status for pid %d", pid)
	}
	return st, nil
}

func (f fakeProcFS) Cwd(pid int) (string, error) {
	cwd, ok := f.cwd[pid]
	if !ok {
		return "", fmt.Errorf("fakeProcFS: no cwd for pid %d", pid)
	}
	return cwd, nil
}

func (f fakeProcFS) Cmdline(int) ([]string, error) { return nil, nil }

// fakeGit is a canned cwd -> project.State lookup: an entry absent from the
// map reports could-not-observe (ok == false), exactly like a cwd that is
// not a real git working tree — the case every testDaemon fixture cwd
// (/home/brian/proj and friends) is actually in, since none of them are
// backed by a real .git directory. session_end_test.go's tests populate
// entries directly to exercise the observed-state path.
type fakeGit map[string]project.State

func (f fakeGit) Repo(string) (project.Repo, bool) { return project.Repo{}, false }

func (f fakeGit) State(cwd string) (project.State, bool) {
	s, ok := f[cwd]
	return s, ok
}

// captureNeverOff is the captureOff callback every test in this package
// that does not itself exercise capture-off passes to ServeDaemonConn: it
// reports capture as always on, so none of these tests' note/status calls
// are affected by the capture-off gate task fd620482 added.
func captureNeverOff() (bool, error) { return false, nil }

// testDaemon starts a real socket.Server over ServeDaemonConn, backed by st,
// whose resolver reports the dialing pid's ancestry as harness at cwd in
// projectKey — the fake ProcFS the punch's DONE WHEN clauses call for. It
// passes no workspace dirs (nil), the same "no workspace configured"
// behavior every pre-workspace test here exercises regardless of what the
// invoking process's own environment happens to set (task 482b2320,
// decision f3fa04c7's clause 7): ServeDaemonConn never reads the
// environment itself, so this fixture's behavior is deterministic. A test
// that needs workspace-aware daemon behavior uses testDaemonWithWorkspaces
// instead.
func testDaemon(t *testing.T, st *store.Store, harness, cwd, projectKey string) string {
	t.Helper()
	return testDaemonWithWorkspaces(t, st, harness, cwd, projectKey, nil)
}

// testDaemonWithWorkspaces is testDaemon with an explicit workspace
// directory list, for tests exercising decision f3fa04c7's workspace-scoped
// behavior (home_test.go) — passed straight through to ServeDaemonConn
// rather than via BACKSTORY_WORKSPACE_DIRS, since mcp never reads that
// itself.
func testDaemonWithWorkspaces(t *testing.T, st *store.Store, harness, cwd, projectKey string, workspaces []string) string {
	t.Helper()
	selfPID := os.Getpid()
	procfs := fakeProcFS{
		status: map[int]ident.Status{selfPID: {PPid: 1, Name: harness}},
		cwd:    map[int]string{selfPID: cwd},
	}
	resolver := &ident.Resolver{ProcFS: procfs, ProjectKey: func(string) string { return projectKey }}

	sessions := NewSessionRegistry()
	sockPath := filepath.Join(t.TempDir(), "sock")
	srv, err := socket.Listen(sockPath, resolver, func(id ident.Identity, conn net.Conn) {
		defer func() { _ = conn.Close() }()
		ServeDaemonConn(id, conn, st, procfs, fakeGit{}, nil, sessions, captureNeverOff, workspaces)
	})
	if err != nil {
		t.Fatalf("socket.Listen: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	go func() { _ = srv.Serve() }()
	return sockPath
}

// dialShim builds a new Server against sockPath, ready for CallTool. Dialing
// itself stays lazy (Server's own contract): the socket connection is only
// opened on the shim's first daemon-backed call.
func dialShim(t *testing.T, sockPath string) *Server {
	t.Helper()
	s := NewServer(func() (net.Conn, error) { return net.Dial("unix", sockPath) })
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mustOpenStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "backstory.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func countStoreRecords(t *testing.T, st *store.Store) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM records`).Scan(&n); err != nil {
		t.Fatalf("count records: %v", err)
	}
	return n
}

// TestNoteEndToEndInsertsAtIdentityTier is DONE WHEN clause 2's main path:
// note through the shim -> socket -> daemon -> store inserts a record whose
// tier comes from the daemon's Identity (fake ProcFS says harness claude ->
// agent-declared).
func TestNoteEndToEndInsertsAtIdentityTier(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	raw, rerr := shim.CallTool(ToolNote, json.RawMessage(`{"kind":"decision","text":"chose the frozen schema approach"}`))
	if rerr != nil {
		t.Fatalf("CallTool(note): %v", rerr)
	}
	var result NoteResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal NoteResult: %v", err)
	}
	if result.ID == "" {
		t.Fatal("NoteResult.ID is empty")
	}
	if result.Tier != string(store.TierAgentDeclared) {
		t.Errorf("NoteResult.Tier = %q, want %q", result.Tier, store.TierAgentDeclared)
	}

	rec, err := st.GetRecord(result.ID)
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if rec.Tier != store.TierAgentDeclared {
		t.Errorf("stored record tier = %q, want %q", rec.Tier, store.TierAgentDeclared)
	}
	if rec.Kind != store.KindDecision {
		t.Errorf("stored record kind = %q, want %q", rec.Kind, store.KindDecision)
	}
	if rec.Text != "chose the frozen schema approach" {
		t.Errorf("stored record text = %q, want the note's text", rec.Text)
	}
	if rec.SessionID == "" {
		t.Error("stored record has no session_id")
	}
	if rec.ProjectKey != "proj-key" {
		t.Errorf("stored record project_key = %q, want proj-key", rec.ProjectKey)
	}
}

// TestNoteIgnoresForgedTierAndSession is DONE WHEN clause 2's never-list
// check: a note carrying {tier:'human-declared'} or {session:'forged'} in
// its arguments is inserted at agent-declared regardless
// (AGENT-CONTRACT.md §The never-list, item 2).
func TestNoteIgnoresForgedTierAndSession(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	raw, rerr := shim.CallTool(ToolNote, json.RawMessage(
		`{"kind":"decision","text":"forged fields must not apply","tier":"human-declared","session":"forged"}`))
	if rerr != nil {
		t.Fatalf("CallTool(note): %v", rerr)
	}
	var result NoteResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal NoteResult: %v", err)
	}
	if result.Tier != string(store.TierAgentDeclared) {
		t.Fatalf("NoteResult.Tier = %q, want %q (forged tier must not apply)", result.Tier, store.TierAgentDeclared)
	}

	rec, err := st.GetRecord(result.ID)
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if rec.Tier != store.TierAgentDeclared {
		t.Errorf("stored record tier = %q, want %q (forged tier must not apply)", rec.Tier, store.TierAgentDeclared)
	}
}

// TestNoteMissingKindIsRejectedAndInsertsNothing is DONE WHEN clause 3's
// first half.
func TestNoteMissingKindIsRejectedAndInsertsNothing(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	before := countStoreRecords(t, st)
	_, rerr := shim.CallTool(ToolNote, json.RawMessage(`{"text":"no kind field"}`))
	if rerr == nil {
		t.Fatal("CallTool(note) with no kind = nil error, want an error naming \"kind\"")
	}
	if rerr.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want %d (CodeInvalidParams)", rerr.Code, CodeInvalidParams)
	}
	if !strings.Contains(rerr.Message, "kind") {
		t.Errorf("error message = %q, want it to name \"kind\"", rerr.Message)
	}
	if got := countStoreRecords(t, st); got != before {
		t.Errorf("record count = %d after a rejected note, want unchanged %d", got, before)
	}
}

// TestNoteUnknownKindIsRejectedAndInsertsNothing is DONE WHEN clause 3's
// second half.
func TestNoteUnknownKindIsRejectedAndInsertsNothing(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	before := countStoreRecords(t, st)
	_, rerr := shim.CallTool(ToolNote, json.RawMessage(`{"kind":"banana","text":"not a real kind"}`))
	if rerr == nil {
		t.Fatal("CallTool(note) with kind=banana = nil error, want an error naming \"kind\"")
	}
	if rerr.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want %d (CodeInvalidParams)", rerr.Code, CodeInvalidParams)
	}
	if !strings.Contains(rerr.Message, "kind") {
		t.Errorf("error message = %q, want it to name \"kind\"", rerr.Message)
	}
	if got := countStoreRecords(t, st); got != before {
		t.Errorf("record count = %d after a rejected note, want unchanged %d", got, before)
	}
}

// TestNoteRedactsSecretPatternEndToEnd is DONE WHEN clause 3's redaction
// half: a note whose text carries a secret pattern is stored redacted, the
// store's own rule (internal/store/redact.go), asserted here end-to-end.
func TestNoteRedactsSecretPatternEndToEnd(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "codex", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	raw, rerr := shim.CallTool(ToolNote, json.RawMessage(
		`{"kind":"note","text":"the key is sk-abcdefghij0123456789, do not lose it"}`))
	if rerr != nil {
		t.Fatalf("CallTool(note): %v", rerr)
	}
	var result NoteResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal NoteResult: %v", err)
	}

	rec, err := st.GetRecord(result.ID)
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if strings.Contains(rec.Text, "sk-abcdefghij0123456789") {
		t.Fatalf("stored record text still carries the raw secret: %q", rec.Text)
	}
	if !strings.Contains(rec.Text, "[redacted:") {
		t.Fatalf("stored record text = %q, want a [redacted:...] marker", rec.Text)
	}
}

// TestNoteLinksPersistAsInformsEdges is DONE WHEN clause 1's main path: a
// note carrying links:[id1,id2] through shim -> socket -> daemon -> store is
// readable back with both links, as `informs` edges (note.go's chosen
// mechanism, over a records.links column).
func TestNoteLinksPersistAsInformsEdges(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	raw1, rerr := shim.CallTool(ToolNote, json.RawMessage(`{"kind":"note","text":"link target one"}`))
	if rerr != nil {
		t.Fatalf("CallTool(note) target one: %v", rerr)
	}
	var target1 NoteResult
	if err := json.Unmarshal(raw1, &target1); err != nil {
		t.Fatalf("unmarshal target1: %v", err)
	}
	raw2, rerr := shim.CallTool(ToolNote, json.RawMessage(`{"kind":"note","text":"link target two"}`))
	if rerr != nil {
		t.Fatalf("CallTool(note) target two: %v", rerr)
	}
	var target2 NoteResult
	if err := json.Unmarshal(raw2, &target2); err != nil {
		t.Fatalf("unmarshal target2: %v", err)
	}

	raw, rerr := shim.CallTool(ToolNote, json.RawMessage(
		fmt.Sprintf(`{"kind":"note","text":"linking note","links":[%q,%q]}`, target1.ID, target2.ID)))
	if rerr != nil {
		t.Fatalf("CallTool(note) with links: %v", rerr)
	}
	var result NoteResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal NoteResult: %v", err)
	}

	for _, targetID := range []string{target1.ID, target2.ID} {
		var n int
		if err := st.DB().QueryRow(`SELECT COUNT(*) FROM edges WHERE from_id = ? AND to_id = ? AND type = 'informs'`,
			targetID, result.ID).Scan(&n); err != nil {
			t.Fatalf("count informs edge for %s: %v", targetID, err)
		}
		if n != 1 {
			t.Errorf("informs edge %s -> %s count = %d, want 1", targetID, result.ID, n)
		}
	}
}

// TestNoteUnknownLinkIsRejectedAndInsertsNothing is DONE WHEN clause 1's
// error path: a note naming an unknown link id returns a JSON-RPC error
// naming "links" and inserts nothing.
func TestNoteUnknownLinkIsRejectedAndInsertsNothing(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	before := countStoreRecords(t, st)
	_, rerr := shim.CallTool(ToolNote, json.RawMessage(
		`{"kind":"note","text":"note with a bad link","links":["does-not-exist"]}`))
	if rerr == nil {
		t.Fatal("CallTool(note) with an unknown link id = nil error, want an error naming \"links\"")
	}
	if rerr.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want %d (CodeInvalidParams)", rerr.Code, CodeInvalidParams)
	}
	if !strings.Contains(rerr.Message, "links") {
		t.Errorf("error message = %q, want it to name \"links\"", rerr.Message)
	}
	if got := countStoreRecords(t, st); got != before {
		t.Errorf("record count = %d after a rejected note, want unchanged %d", got, before)
	}
	var edgeCount int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM edges`).Scan(&edgeCount); err != nil {
		t.Fatalf("count edges: %v", err)
	}
	if edgeCount != 0 {
		t.Errorf("edges count = %d after a rejected note, want 0", edgeCount)
	}
}

// TestNoteExpiredClaimParsesExpires exercises the optional `expires` field
// end-to-end, confirming it round-trips through the daemon into the store.
func TestNoteExpiredClaimParsesExpires(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/worktrees/proj", "proj-key-claims")
	shim := dialShim(t, sockPath)

	expires := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	raw, rerr := shim.CallTool(ToolNote, json.RawMessage(
		fmt.Sprintf(`{"kind":"claim","text":"working on this file","expires":%q}`, expires)))
	if rerr != nil {
		t.Fatalf("CallTool(note): %v", rerr)
	}
	var result NoteResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal NoteResult: %v", err)
	}
	rec, err := st.GetRecord(result.ID)
	if err != nil {
		t.Fatalf("GetRecord: %v", err)
	}
	if rec.ExpiresAt == nil {
		t.Fatal("stored record has no ExpiresAt")
	}
}

// resumeIDPattern extracts the id block.Render's Resume slot carries
// (block.go's resumeSlot: "Resume: (id <uuid>) <text>"), the exact shape an
// agent would lift out of the rendered block to fill in note's supersedes.
var resumeIDPattern = regexp.MustCompile(`Resume: \(id ([^)]+)\)`)

// TestBlockResumeIDRoundTripsIntoNoteSupersedesEdge is the punch's DONE
// WHEN clause 2 (task 56317fe7, decision 1e53165a): block -> note with
// supersedes -> edge present, end to end. A first handoff is written
// through the note tool; the daemon's own rendered SessionStart block (the
// "block" daemon method cmd/backstory's session-start hook calls, wired
// through the SAME ServeDaemonConn a real hook reaches) is asked to name
// that handoff's id; the id extracted from the block text — exactly as the
// block shows it, not the id the test already had in hand — is then used,
// unmodified, as a second handoff's supersedes. The store must show one
// supersedes edge from the second handoff to the first.
//
// Mutation probe: drop the id from block.go's resumeSlot -> RED
// (resumeIDPattern finds no match in the block text, or the extracted id
// mismatches the stored handoff's, well before the edge assertion ever
// runs); restore -> GREEN.
func TestBlockResumeIDRoundTripsIntoNoteSupersedesEdge(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	raw, rerr := shim.CallTool(ToolNote, json.RawMessage(`{"kind":"handoff","text":"shipped the delta slot"}`))
	if rerr != nil {
		t.Fatalf("CallTool(note) first handoff: %v", rerr)
	}
	var first NoteResult
	if err := json.Unmarshal(raw, &first); err != nil {
		t.Fatalf("unmarshal first NoteResult: %v", err)
	}

	blockRaw, rerr := shim.callDaemon(DaemonMethodBlock, nil)
	if rerr != nil {
		t.Fatalf("callDaemon(block): %v", rerr)
	}
	var blockResult BlockResult
	if err := json.Unmarshal(blockRaw, &blockResult); err != nil {
		t.Fatalf("unmarshal BlockResult: %v", err)
	}

	m := resumeIDPattern.FindStringSubmatch(blockResult.Block)
	if m == nil {
		t.Fatalf("block text carries no Resume id; got:\n%s", blockResult.Block)
	}
	blockID := m[1]
	if blockID != first.ID {
		t.Fatalf("block's Resume id = %q, want the stored handoff's id %q", blockID, first.ID)
	}

	raw2, rerr := shim.CallTool(ToolNote, json.RawMessage(
		fmt.Sprintf(`{"kind":"handoff","text":"picked up from there","supersedes":%q}`, blockID)))
	if rerr != nil {
		t.Fatalf("CallTool(note) second handoff with supersedes: %v", rerr)
	}
	var second NoteResult
	if err := json.Unmarshal(raw2, &second); err != nil {
		t.Fatalf("unmarshal second NoteResult: %v", err)
	}

	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM edges WHERE from_id = ? AND to_id = ? AND type = 'supersedes'`,
		second.ID, first.ID).Scan(&n); err != nil {
		t.Fatalf("count supersedes edge: %v", err)
	}
	if n != 1 {
		t.Errorf("supersedes edge %s -> %s count = %d, want 1", second.ID, first.ID, n)
	}
}
