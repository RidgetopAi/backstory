package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/mcp"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// locationFixture is the Hermes Desktop shape: one harness process whose own
// cwd is HOME serving chats that live in HOME/projects/foo, a git repo.
type locationFixture struct {
	t          *testing.T
	sockPath   string
	dbPath     string
	harnessBin string
	home       string
	foo        string
	fooKey     string
}

func newLocationFixture(t *testing.T) *locationFixture {
	t.Helper()
	bin := buildBackstory(t)
	dbPath, runtimeDir, _ := startTestDaemon(t, bin)
	home := t.TempDir()
	foo := filepath.Join(home, "projects", "foo")
	if err := os.MkdirAll(foo, 0o700); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, foo)
	return &locationFixture{
		t: t, dbPath: dbPath, home: home, foo: foo,
		sockPath:   filepath.Join(runtimeDir, "backstory", "sock"),
		harnessBin: buildSessionHarness(t, harnessName),
		fooKey:     project.Key(foo, project.RealGit{}, nil),
	}
}

func (f *locationFixture) run(dir string, steps ...sessionHarnessStep) []json.RawMessage {
	f.t.Helper()
	return runSessionHarness(f.t, f.harnessBin, f.sockPath, dir, steps)
}

func (f *locationFixture) block(r json.RawMessage) string {
	f.t.Helper()
	var b mcp.BlockResult
	if err := json.Unmarshal(r, &b); err != nil {
		f.t.Fatalf("decode block: %v", err)
	}
	return b.Block
}

func (f *locationFixture) sessionStatus(r json.RawMessage) string {
	f.t.Helper()
	var s mcp.StatusResult
	if err := json.Unmarshal(r, &s); err != nil {
		f.t.Fatalf("decode status: %v", err)
	}
	return s.Session
}

// sessionRow returns (project_key, cwd) of one session row.
func (f *locationFixture) sessionRow(id string) (string, string) {
	f.t.Helper()
	s := mustOpenTestStore(f.t, f.dbPath)
	var key, cwd string
	if err := s.DB().QueryRow(`SELECT project_key, cwd FROM sessions WHERE id = ?`, id).Scan(&key, &cwd); err != nil {
		f.t.Fatalf("session %s: %v", id, err)
	}
	return key, cwd
}

func locParams(loc string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"location": loc})
	return b
}

// noteInFoo files a decision under foo from a harness whose own cwd is foo.
func (f *locationFixture) noteInFoo(text string) {
	f.t.Helper()
	p, _ := json.Marshal(map[string]string{"kind": "handoff", "text": text, "next": "carry on in foo"})
	f.run(f.foo, sessionHarnessStep{Session: "cli", Method: "note", Params: p})
}

// Clause 1: a plugin block call with location files the session under that
// folder's project and returns its block; without location, under HOME.
func TestPluginLocationSelectsProject(t *testing.T) {
	f := newLocationFixture(t)
	f.noteInFoo("foo uses sqlite for the zebra index")

	res := f.run(f.home,
		sessionHarnessStep{Session: "chat1", Method: mcp.DaemonMethodBlock, Params: locParams(f.foo)},
		sessionHarnessStep{Session: "chat1", Method: "status"},
	)
	if got := f.block(res[0]); !strings.Contains(got, "zebra index") {
		t.Errorf("located block is not foo's (no foo decision in it):\n%s", got)
	}
	key, cwd := f.sessionRow(f.sessionStatus(res[1]))
	if key != f.fooKey {
		t.Errorf("located session project_key = %q, want foo's %q", key, f.fooKey)
	}
	if cwd != f.foo {
		t.Errorf("located session cwd = %q, want %q", cwd, f.foo)
	}

	res = f.run(f.home,
		sessionHarnessStep{Session: "chat2", Method: mcp.DaemonMethodBlock},
		sessionHarnessStep{Session: "chat2", Method: "status"},
	)
	if got := f.block(res[0]); strings.Contains(got, "zebra index") {
		t.Errorf("block without location leaked foo's decision:\n%s", got)
	}
	key, cwd = f.sessionRow(f.sessionStatus(res[1]))
	if key == f.fooKey || cwd != f.home {
		t.Errorf("unlocated session = (%q, %q), want HOME's (not foo's key, cwd %q)", key, cwd, f.home)
	}
}

// Clause 1 (post_tool_use half): the event files under the located project.
func TestPluginLocationOnPostToolUse(t *testing.T) {
	f := newLocationFixture(t)
	p, _ := json.Marshal(map[string]string{"tool_name": "Read", "tool_use_id": "t1", "location": f.foo})
	f.run(f.home, sessionHarnessStep{Session: "chat1", Method: mcp.DaemonMethodPostToolUse, Params: p})
	s := mustOpenTestStore(t, f.dbPath)
	if ev := queryEventSessionsForProject(t, s, f.fooKey); len(ev) != 1 {
		t.Fatalf("foo has %d events, want 1: %#v", len(ev), ev)
	}
}

// Clause 2: a missing folder and one owned by another uid are ignored.
func TestPluginLocationIgnoredWhenInvalid(t *testing.T) {
	f := newLocationFixture(t)
	other := filepath.Join(f.home, "other-owner")
	if err := os.Mkdir(other, 0o700); err != nil {
		t.Fatal(err)
	}
	if os.Getuid() == 0 {
		if err := os.Chown(other, 65534, 65534); err != nil {
			t.Fatal(err)
		}
	} else {
		other = "/" // a directory owned by root, not by this uid
	}
	for name, loc := range map[string]string{
		"missing":   filepath.Join(f.home, "no-such-folder"),
		"other-uid": other,
		"relative":  "projects/foo",
	} {
		res := f.run(f.home,
			sessionHarnessStep{Session: name, Method: mcp.DaemonMethodBlock, Params: locParams(loc)},
			sessionHarnessStep{Session: name, Method: "status"},
		)
		_ = res
		key, cwd := f.sessionRow(f.sessionStatus(res[1]))
		if key == f.fooKey || cwd != f.home {
			t.Errorf("%s: session = (%q, %q), want the /proc cwd %q", name, key, cwd, f.home)
		}
	}
}

// Clause 3: a model tool call carrying location/cwd/project never moves the
// session, and the record lands under the session's own project.
func TestModelToolCallLocationIgnored(t *testing.T) {
	f := newLocationFixture(t)
	for _, method := range []string{"note", "recall"} {
		arg := map[string]any{"kind": "decision", "text": "homebound " + method, "location": f.foo, "cwd": f.foo, "project": f.fooKey}
		if method == "recall" {
			arg = map[string]any{"query": "homebound", "location": f.foo, "cwd": f.foo, "project": f.fooKey}
		}
		p, _ := json.Marshal(arg)
		res := f.run(f.home,
			sessionHarnessStep{Session: "m-" + method, Method: method, Params: p},
			sessionHarnessStep{Session: "m-" + method, Method: "status"},
		)
		key, cwd := f.sessionRow(f.sessionStatus(res[1]))
		if key == f.fooKey || cwd != f.home {
			t.Errorf("%s: session = (%q, %q), want HOME's", method, key, cwd)
		}
	}
	s := mustOpenTestStore(t, f.dbPath)
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM records WHERE project_key = ?`, f.fooKey).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("foo has %d records from model-supplied location, want 0", n)
	}
	_ = store.OriginLive
}

// A chat's location-less model tool calls follow the folder its plugin
// reported on block, so notes file under the chat's project.
func TestToolCallFollowsChatLocation(t *testing.T) {
	f := newLocationFixture(t)
	p, _ := json.Marshal(map[string]string{"kind": "decision", "text": "chat note about yak shaving"})
	f.run(f.home,
		sessionHarnessStep{Session: "chat1", Method: mcp.DaemonMethodBlock, Params: locParams(f.foo)},
		sessionHarnessStep{Session: "chat1", Method: "note", Params: p},
	)
	s := mustOpenTestStore(t, f.dbPath)
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM records WHERE project_key = ?`, f.fooKey).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("foo has %d records, want the chat's note", n)
	}
}
