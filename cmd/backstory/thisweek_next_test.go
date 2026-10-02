package main

import (
	"bytes"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/mcp"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/socket"
	"github.com/RidgetopAi/backstory/internal/store"
)

// nextProcFS is a synthetic /proc tree keyed on this test process's own pid.
type nextProcFS struct{}

func (f nextProcFS) Status(pid int) (ident.Status, error) {
	return ident.Status{PPid: 1, Name: "claude"}, nil
}
func (f nextProcFS) Cwd(int) (string, error)       { return "/home/brian/nextproj", nil }
func (f nextProcFS) Cmdline(int) ([]string, error) { return nil, nil }

// TestHandoffNextRoundTripsMCPToThisWeekJSON drives the MCP shim like a
// spec-shaped client (note{kind:handoff,text,next}), then reads
// `this-week --json` back: the project's handoff_next is the stored next.
// The three refusals leave the store untouched.
func TestHandoffNextRoundTripsMCPToThisWeekJSON(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataDir)
	t.Setenv("HOME", "/home/brian")
	t.Setenv("BACKSTORY_WORKSPACE_DIRS", fixtureWorkspaceDir)
	st, err := store.Open(filepath.Join(dataDir, "backstory", "backstory.db"), nil, nil)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	procfs := nextProcFS{}
	resolver := &ident.Resolver{ProcFS: procfs, ProjectKey: func(string) string { return "next-proj" }}
	sessions := mcp.NewSessionRegistry()
	sockPath := filepath.Join(t.TempDir(), "sock")
	srv, err := socket.Listen(sockPath, resolver, func(id ident.Identity, conn net.Conn) {
		defer func() { _ = conn.Close() }()
		mcp.ServeDaemonConn(id, conn, st, procfs, project.RealGit{}, nil, sessions, func() (bool, error) { return false, nil }, nil)
	})
	if err != nil {
		t.Fatalf("socket.Listen: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	go func() { _ = srv.Serve() }()
	shim := mcp.NewServer(func() (net.Conn, error) { return net.Dial("unix", sockPath) })
	t.Cleanup(func() { _ = shim.Close() })

	countRecords := func() int {
		var n int
		if err := st.DB().QueryRow(`SELECT COUNT(*) FROM records`).Scan(&n); err != nil {
			t.Fatalf("count records: %v", err)
		}
		return n
	}
	refused := []struct {
		name, args, rule string
	}{
		{"decision", `{"kind":"decision","text":"x","next":"Do it"}`, "handoff"},
		{"newline", `{"kind":"handoff","text":"x","next":"a\nb"}`, "newline"},
		{"201 chars", `{"kind":"handoff","text":"x","next":"` + strings.Repeat("n", 201) + `"}`, "200"},
	}
	for _, c := range refused {
		before := countRecords()
		_, rerr := shim.CallTool(mcp.ToolNote, json.RawMessage(c.args))
		if rerr == nil || !strings.Contains(rerr.Message, c.rule) || !strings.Contains(rerr.Message, "next") {
			t.Errorf("%s: err = %v, want an error naming next and %q", c.name, rerr, c.rule)
		}
		if got := countRecords(); got != before {
			t.Errorf("%s: record count %d -> %d, want unchanged", c.name, before, got)
		}
	}

	if _, rerr := shim.CallTool(mcp.ToolNote, json.RawMessage(`{"kind":"handoff","text":"Long handoff…","next":"Wire the bar widget"}`)); rerr != nil {
		t.Fatalf("note handoff with next: %v", rerr)
	}

	orig := thisWeekNow
	thisWeekNow = time.Now
	t.Cleanup(func() { thisWeekNow = orig })
	var out, errBuf bytes.Buffer
	if code := run([]string{"this-week", "--json"}, bytes.NewReader(nil), &out, &errBuf); code != 0 {
		t.Fatalf("this-week exit %d: %s", code, errBuf.String())
	}
	var parsed thisWeekOutputJSON
	if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
		t.Fatalf("parse: %v\n%s", err, out.String())
	}
	var found *projectSummaryJSON
	for i := range parsed.WhereLeftOff {
		if p := parsed.WhereLeftOff[i].Project; p != nil && p.ProjectKey == "next-proj" {
			found = p
		}
	}
	if found == nil {
		t.Fatalf("next-proj missing from where_left_off:\n%s", out.String())
	}
	if found.HandoffNext != "Wire the bar widget" {
		t.Errorf("handoff_next = %q, want %q", found.HandoffNext, "Wire the bar widget")
	}
	if found.LastAgent != "claude" {
		t.Errorf("last_agent = %q, want claude", found.LastAgent)
	}
}
