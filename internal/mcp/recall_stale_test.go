package mcp

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// markOf returns the trust mark recall renders for id.
func markOf(t *testing.T, s *Server, id string) string {
	t.Helper()
	res := callRecall(t, s, json.RawMessage(`{"altitude":"full","budget_tokens":100000}`))
	for _, it := range res.Items {
		if it.ID == id {
			return it.Mark
		}
	}
	t.Fatalf("recall did not return %s", id)
	return ""
}

func recallShim(t *testing.T) *Server {
	t.Helper()
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	s := NewServer(func() (net.Conn, error) { return net.Dial("unix", sockPath) })
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// A handoff the engine classifies possibly-stale (a later record in its
// project) must not render as `current`.
func TestRecallPossiblyStaleHandoffIsNotCurrent(t *testing.T) {
	s := recallShim(t)
	noteAbout := func(kind, text string) string {
		raw, rerr := s.CallTool(ToolNote, json.RawMessage(fmt.Sprintf(
			`{"kind":%q,"text":%q,"about":["a.go"]}`, kind, text)))
		if rerr != nil {
			t.Fatalf("note: %v", rerr)
		}
		var r NoteResult
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatal(err)
		}
		return r.ID
	}
	handoff := noteAbout("handoff", "state: mid-refactor")
	if got := markOf(t, s, handoff); got != "current" {
		t.Fatalf("fresh handoff mark = %q, want current", got)
	}
	noteAbout("decision", "a later record about the same file")
	got := markOf(t, s, handoff)
	if got == "current" || !strings.Contains(got, "possibly stale") {
		t.Fatalf("stale handoff mark = %q, want a possibly-stale mark", got)
	}
}

// An expired claim is marked expired; an unexpired one still reads current.
func TestRecallExpiredClaimIsNotCurrent(t *testing.T) {
	s := recallShim(t)
	mk := func(text string, exp time.Time) string {
		raw, rerr := s.CallTool(ToolNote, json.RawMessage(fmt.Sprintf(
			`{"kind":"claim","text":%q,"expires":%q}`, text, exp.UTC().Format(time.RFC3339))))
		if rerr != nil {
			t.Fatalf("note: %v", rerr)
		}
		var r NoteResult
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatal(err)
		}
		return r.ID
	}
	expired := mk("timeline not implemented", time.Now().Add(-time.Hour))
	live := mk("still true", time.Now().Add(time.Hour))
	if got := markOf(t, s, expired); got == "current" || !strings.Contains(got, "expired") {
		t.Errorf("expired claim mark = %q, want an expired mark", got)
	}
	if got := markOf(t, s, live); got != "current" {
		t.Errorf("unexpired claim mark = %q, want current", got)
	}
}

// The note tool's supersedes description tells agents to set it on replacement.
func TestNoteSupersedesDescriptionInstructsSupersede(t *testing.T) {
	for _, tool := range ToolsV0() {
		if tool.Name != ToolNote {
			continue
		}
		raw, _ := json.Marshal(tool)
		if !strings.Contains(string(raw), "replaces or corrects an earlier one") {
			t.Fatalf("note tool schema lacks supersede guidance: %s", raw)
		}
		return
	}
	t.Fatal("note tool not found")
}
