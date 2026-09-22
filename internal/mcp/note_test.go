package mcp

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/ident"
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

// testDaemon starts a real socket.Server over ServeDaemonConn, backed by st,
// whose resolver reports the dialing pid's ancestry as harness at cwd in
// projectKey — the fake ProcFS the punch's DONE WHEN clauses call for.
func testDaemon(t *testing.T, st *store.Store, harness, cwd, projectKey string) string {
	t.Helper()
	selfPID := os.Getpid()
	procfs := fakeProcFS{
		status: map[int]ident.Status{selfPID: {PPid: 1, Name: harness}},
		cwd:    map[int]string{selfPID: cwd},
	}
	resolver := &ident.Resolver{ProcFS: procfs, ProjectKey: func(string) string { return projectKey }}

	sockPath := filepath.Join(t.TempDir(), "sock")
	srv, err := socket.Listen(sockPath, resolver, func(id ident.Identity, conn net.Conn) {
		defer func() { _ = conn.Close() }()
		ServeDaemonConn(id, conn, st, procfs, nil)
	})
	if err != nil {
		t.Fatalf("socket.Listen: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	go func() { _ = srv.Serve() }()
	return sockPath
}

// dialShim connects a new Server to sockPath, ready for CallTool.
func dialShim(t *testing.T, sockPath string) *Server {
	t.Helper()
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial %s: %v", sockPath, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return NewServer(conn)
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
