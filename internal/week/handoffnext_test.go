package week

import (
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

func TestHandoffNextDerivedFromFirstLine(t *testing.T) {
	cases := []struct {
		name string
		rec  store.Record
		want string
	}{
		{"star, HANDOFF, date, period", store.Record{Text: "★ HANDOFF 2026-10-01. Brian confirmed the change"}, "Brian confirmed the change"},
		{"em dashes and time", store.Record{Text: "★ HANDOFF — 2026-09-25 03:40Z — ship it"}, "ship it"},
		{"no boilerplate stays whole", store.Record{Text: "MODE: debug. NEXT: run x"}, "MODE: debug. NEXT: run x"},
		{"only the first line", store.Record{Text: "★ HANDOFF 2026-10-01: first\nsecond line"}, "first"},
		{"stored next wins", store.Record{Text: "★ HANDOFF 2026-10-01. boilerplate", Next: "Wire the bar widget"}, "Wire the bar widget"},
		{"pure boilerplate falls back to the line", store.Record{Text: "★ HANDOFF 2026-10-01"}, "★ HANDOFF 2026-10-01"},
	}
	for _, c := range cases {
		if got := handoffNext(c.rec); got != c.want {
			t.Errorf("%s: handoffNext = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestBuildHandoffNextEmptyWithoutHandoff(t *testing.T) {
	st := openTestStore(t)
	upsertProject(t, st, "proj-nohandoff", "/home/brian/nohandoff")
	sid := startSession(t, st, "sess-nh", "proj-nohandoff", "/home/brian/nohandoff", fixtureNow.Add(-time.Hour))
	appendEvent(t, st, store.Event{TS: fixtureNow.Add(-time.Hour), Kind: "tool.use", SessionID: sid, Source: "shell", Payload: `{}`})
	res, err := Build(Params{Store: st, Now: fixtureNow})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, r := range res.WhereLeftOff {
		if r.Project.ProjectKey == "proj-nohandoff" && r.Project.HandoffNext != "" {
			t.Errorf("HandoffNext = %q, want \"\" with no handoff", r.Project.HandoffNext)
		}
	}
}

func TestBuildLastAgentIsNewestSessionsAgent(t *testing.T) {
	st := openTestStore(t)
	upsertProject(t, st, "proj-agents", "/home/brian/agents")
	upsertProject(t, st, "proj-grp-a", "/home/brian/grp-a")
	upsertProject(t, st, "proj-grp-b", "/home/brian/grp-b")
	start := func(id, agent, key, cwd string, at time.Time) {
		t.Helper()
		sid, err := st.StartSession(store.StartSessionParams{ID: id, Agent: agent, CWD: cwd, ProjectKey: key, StartedAt: at, Origin: store.OriginLive})
		if err != nil {
			t.Fatalf("StartSession(%s): %v", id, err)
		}
		appendEvent(t, st, store.Event{TS: at, Kind: "tool.use", SessionID: sid, Source: "shell", Payload: `{}`})
	}
	start("s-old", "codex", "proj-agents", "/home/brian/agents", fixtureNow.Add(-48*time.Hour))
	start("s-new", "claude", "proj-agents", "/home/brian/agents", fixtureNow.Add(-2*time.Hour))
	start("s-ga-old", "claude", "proj-grp-a", "/home/brian/grp-a", fixtureNow.Add(-30*time.Hour))
	start("s-ga-new", "codex", "proj-grp-a", "/home/brian/grp-a", fixtureNow.Add(-3*time.Hour))
	start("s-gb", "pi", "proj-grp-b", "/home/brian/grp-b", fixtureNow.Add(-4*time.Hour))
	human := store.Identity{Kind: store.IdentityHuman, Actor: "human"}
	for _, k := range []string{"proj-grp-a", "proj-grp-b"} {
		if err := st.SetProjectGroup(human, "grp", k); err != nil {
			t.Fatalf("SetProjectGroup: %v", err)
		}
	}
	if _, err := st.InsertRecord(store.InsertRecordParams{Identity: agentIdentity, Kind: store.KindHandoff, Text: "★ HANDOFF 2026-03-09. keep going", SessionID: "s-gb", ProjectKey: "proj-grp-b"}); err != nil {
		t.Fatalf("InsertRecord: %v", err)
	}

	res, err := Build(Params{Store: st, Now: fixtureNow})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := map[string]ProjectSummary{}
	for _, r := range res.WhereLeftOff {
		if r.Group == "" {
			got[r.Project.ProjectKey] = r.Project
		}
		for _, c := range r.Children {
			got[c.ProjectKey] = c
		}
	}
	for key, want := range map[string]string{"proj-agents": "claude", "proj-grp-a": "codex", "proj-grp-b": "pi"} {
		if got[key].LastAgent != want {
			t.Errorf("%s LastAgent = %q, want %q", key, got[key].LastAgent, want)
		}
	}
	if got["proj-grp-b"].HandoffNext != "keep going" {
		t.Errorf("group child HandoffNext = %q, want %q", got["proj-grp-b"].HandoffNext, "keep going")
	}
}
