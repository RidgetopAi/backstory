package mcp

import (
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/store"
)

// noteText inserts a kind=kind record with text through the shim and
// returns its id, failing the test on any error.
func noteText(t *testing.T, shim *Server, kind, text string) string {
	t.Helper()
	raw, rerr := shim.CallTool(ToolNote, json.RawMessage(fmt.Sprintf(`{"kind":%q,"text":%q}`, kind, text)))
	if rerr != nil {
		t.Fatalf("CallTool(note, kind=%s): %v", kind, rerr)
	}
	var result NoteResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal NoteResult: %v", err)
	}
	return result.ID
}

// noteSuperseding inserts a kind=kind record that supersedes supersedesID
// and returns its id.
func noteSuperseding(t *testing.T, shim *Server, kind, text, supersedesID string) string {
	t.Helper()
	raw, rerr := shim.CallTool(ToolNote, json.RawMessage(fmt.Sprintf(
		`{"kind":%q,"text":%q,"supersedes":%q}`, kind, text, supersedesID)))
	if rerr != nil {
		t.Fatalf("CallTool(note, kind=%s, supersedes=%s): %v", kind, supersedesID, rerr)
	}
	var result NoteResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal NoteResult: %v", err)
	}
	return result.ID
}

// noteLinkedTo inserts a kind=kind record carrying an `informs` edge to
// linkedID (note's "links" field) and returns its id — used to give a
// record anchor something to walk to.
func noteLinkedTo(t *testing.T, shim *Server, kind, text, linkedID string) string {
	t.Helper()
	raw, rerr := shim.CallTool(ToolNote, json.RawMessage(fmt.Sprintf(
		`{"kind":%q,"text":%q,"links":[%q]}`, kind, text, linkedID)))
	if rerr != nil {
		t.Fatalf("CallTool(note, kind=%s, links=[%s]): %v", kind, linkedID, rerr)
	}
	var result NoteResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal NoteResult: %v", err)
	}
	return result.ID
}

// callRecall drives ToolRecall through shim's CallTool shortcut (daemon's
// raw JSON result, not the CallToolResult envelope) and unmarshals it as a
// RecallResult, failing the test on any error.
func callRecall(t *testing.T, shim *Server, args json.RawMessage) RecallResult {
	t.Helper()
	raw, rerr := shim.CallTool(ToolRecall, args)
	if rerr != nil {
		t.Fatalf("CallTool(recall): %v", rerr)
	}
	var result RecallResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal RecallResult: %v", err)
	}
	return result
}

func recallItemIDs(items []RecallItem) []string {
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	return ids
}

func containsID(items []RecallItem, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

// TestRecallWithNoQueryAnchorsOnCallersProjectNewestFirst is DONE WHEN
// clause 1/2's project-anchor path: recall with no query returns every
// record in the caller's own project, newest first by sequence (recall.
// ProjectAnchor's own ordering rule) — not the v0 stub's
// handoff-always-first special case, which this punch's engine call
// replaces outright.
func TestRecallWithNoQueryAnchorsOnCallersProjectNewestFirst(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	decision1ID := noteText(t, shim, "decision", "chose approach A")
	decision2ID := noteText(t, shim, "decision", "chose approach B")
	handoffID := noteText(t, shim, "handoff", "state: ready for review")

	result := callRecall(t, shim, nil)

	if result.ProjectKey != "proj-key" {
		t.Errorf("ProjectKey = %q, want proj-key", result.ProjectKey)
	}
	if result.Anchor != "project" {
		t.Errorf("Anchor = %q, want project", result.Anchor)
	}
	if result.Altitude != "summary" {
		t.Errorf("Altitude = %q, want the default summary", result.Altitude)
	}

	wantOrder := []string{handoffID, decision2ID, decision1ID}
	gotOrder := recallItemIDs(result.Items)
	if len(gotOrder) != len(wantOrder) {
		t.Fatalf("Items ids = %v, want %v (newest first)", gotOrder, wantOrder)
	}
	for i, id := range wantOrder {
		if gotOrder[i] != id {
			t.Errorf("Items[%d].ID = %q, want %q (newest first by sequence)", i, gotOrder[i], id)
		}
	}
	for _, item := range result.Items {
		if item.Tier != string(store.TierAgentDeclared) {
			t.Errorf("item %s Tier = %q, want %q", item.ID, item.Tier, store.TierAgentDeclared)
		}
		if item.Mark != "current" {
			t.Errorf("item %s Mark = %q, want current", item.ID, item.Mark)
		}
	}
}

// TestRecallProjectAnchorExcludesOtherProjectRecords is DONE WHEN clause 2's
// isolation requirement for the default (project) anchor: a record from a
// different project seeded in the same store does not appear.
func TestRecallProjectAnchorExcludesOtherProjectRecords(t *testing.T) {
	st := mustOpenStore(t)
	sockPathA := testDaemon(t, st, "claude", "/home/brian/proj-a", "proj-a")
	sockPathB := testDaemon(t, st, "claude", "/home/brian/proj-b", "proj-b")
	shimA := dialShim(t, sockPathA)
	shimB := dialShim(t, sockPathB)

	decisionA := noteText(t, shimA, "decision", "A's decision")
	decisionB := noteText(t, shimB, "decision", "B's private decision")

	resultA := callRecall(t, shimA, nil)
	if resultA.ProjectKey != "proj-a" {
		t.Errorf("ProjectKey = %q, want proj-a", resultA.ProjectKey)
	}
	if !containsID(resultA.Items, decisionA) {
		t.Fatalf("Items = %+v, want A's own decision %s", resultA.Items, decisionA)
	}
	if containsID(resultA.Items, decisionB) {
		t.Errorf("Items = %+v, leaked B's decision %s into A's project anchor", resultA.Items, decisionB)
	}

	raw, err := json.Marshal(resultA)
	if err != nil {
		t.Fatalf("marshal resultA: %v", err)
	}
	if strings.Contains(string(raw), "B's private") {
		t.Errorf("recall for proj-a leaked B's project content: %s", raw)
	}
}

// TestRecallOnEmptyProjectReturnsHonestEmptyResult is DONE WHEN clause 3
// (the engine's own honest-empty rule, PLAN.md §Phase 4): a project with no
// records returns an empty item list naming the project, not an error and
// not not-implemented.
func TestRecallOnEmptyProjectReturnsHonestEmptyResult(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/empty-proj", "empty-proj-key")
	shim := dialShim(t, sockPath)

	result := callRecall(t, shim, nil)

	if result.ProjectKey != "empty-proj-key" {
		t.Errorf("ProjectKey = %q, want empty-proj-key", result.ProjectKey)
	}
	if result.Anchor != "project" {
		t.Errorf("Anchor = %q, want project", result.Anchor)
	}
	if len(result.Items) != 0 {
		t.Errorf("Items = %+v, want empty", result.Items)
	}
}

// TestRecallRecordIDQueryReturnsNeighbourhood is DONE WHEN clause 2's
// record-anchor path: a query naming an existing record id returns that
// record plus what its edges reach (recall.RecordAnchor's edge walk), not
// just the named record alone, and not the whole project ledger.
func TestRecallRecordIDQueryReturnsNeighbourhood(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	rootID := noteText(t, shim, "decision", "root decision")
	linkedID := noteLinkedTo(t, shim, "outcome", "outcome linked to the root", rootID)
	unrelatedID := noteText(t, shim, "decision", "an unrelated decision")

	args, err := json.Marshal(RecallParams{Query: rootID})
	if err != nil {
		t.Fatalf("marshal RecallParams: %v", err)
	}
	result := callRecall(t, shim, args)

	if result.Anchor != "record" {
		t.Errorf("Anchor = %q, want record", result.Anchor)
	}
	if !containsID(result.Items, rootID) {
		t.Errorf("Items = %+v, want the anchor record %s itself", result.Items, rootID)
	}
	if !containsID(result.Items, linkedID) {
		t.Errorf("Items = %+v, want the linked record %s (one edge hop away)", result.Items, linkedID)
	}
	if containsID(result.Items, unrelatedID) {
		t.Errorf("Items = %+v, want the unrelated record %s excluded", result.Items, unrelatedID)
	}
}

// TestRecallFreeTextQueryScopedToCallersProject is DONE WHEN clause 2's
// free-text path: a query that names no record id runs an FTS match scoped
// to the caller's own observed project — a matching record seeded in
// another project is absent from the result, never leaked.
//
// RA-MUTATION-PROBE: resolveAnchor's free-text branch (mcp/recall.go)
// `recall.TextAnchor(projectKey, q)` with projectKey replaced by ""
// (simulating project scoping dropped) -> RED (this test: the caller's own
// expected match, quokkaA, disappears because SearchRecordsInProject("", ...)
// matches no project's records); restored -> GREEN. See this task's proof.
func TestRecallFreeTextQueryScopedToCallersProject(t *testing.T) {
	st := mustOpenStore(t)
	sockPathA := testDaemon(t, st, "claude", "/home/brian/proj-a", "proj-a")
	sockPathB := testDaemon(t, st, "claude", "/home/brian/proj-b", "proj-b")
	shimA := dialShim(t, sockPathA)
	shimB := dialShim(t, sockPathB)

	quokkaA := noteText(t, shimA, "note", "a quokka wandered into camp at dawn")
	quokkaB := noteText(t, shimB, "note", "a quokka also wandered into this other camp")

	args, err := json.Marshal(RecallParams{Query: "quokka"})
	if err != nil {
		t.Fatalf("marshal RecallParams: %v", err)
	}
	result := callRecall(t, shimA, args)

	if result.Anchor != "text" {
		t.Errorf("Anchor = %q, want text", result.Anchor)
	}
	if !containsID(result.Items, quokkaA) {
		t.Errorf("Items = %+v, want proj-a's own match %s", result.Items, quokkaA)
	}
	if containsID(result.Items, quokkaB) {
		t.Errorf("Items = %+v, leaked proj-b's match %s into proj-a's free-text recall", result.Items, quokkaB)
	}
}

// TestRecallEmptyProjectMessageNamesDisplayNameAndSessionCount is this
// punch's DONE WHEN clause 1's main path (task b172e778, real use
// 2026-09-28): recall with no query on a project that has a session (the
// daemon connection itself starts one) but no records ever written returns
// a response naming both the project's key and its human display name, and
// an empty Items list carries a Message explaining why: "no records yet for
// <display_name> (N sessions observed)" — the line an agent reads to tell
// "no history here yet" apart from "wrong project" (which would instead
// show a different project's key/display name) or "0 sessions" (which would
// mean the daemon never even saw this project before).
//
// RA-MUTATION-PROBE: drop displayName from emptyResultMessage's fmt.Sprintf
// (mcp/recall.go), e.g. hardcode a fixed placeholder instead of %s -> RED
// (this test's want string, built from result.DisplayName itself, no longer
// matches); restore -> GREEN. See this task's proof.
func TestRecallEmptyProjectMessageNamesDisplayNameAndSessionCount(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/empty-proj", "empty-proj-key")
	shim := dialShim(t, sockPath)

	result := callRecall(t, shim, nil)

	if result.ProjectKey != "empty-proj-key" {
		t.Errorf("ProjectKey = %q, want empty-proj-key", result.ProjectKey)
	}
	if result.DisplayName == "" {
		t.Fatal("DisplayName is empty, want a human-readable project name")
	}
	if len(result.Items) != 0 {
		t.Fatalf("Items = %+v, want empty (no records were ever written)", result.Items)
	}

	n, err := st.SessionCountForProject("empty-proj-key")
	if err != nil {
		t.Fatalf("SessionCountForProject: %v", err)
	}
	if n == 0 {
		t.Fatal("SessionCountForProject = 0, want at least the connecting daemon session")
	}
	want := fmt.Sprintf("no records yet for %s (%d sessions observed)", result.DisplayName, n)
	if result.Message != want {
		t.Errorf("Message = %q, want %q", result.Message, want)
	}
}

// TestRecallQueryMatchingNothingSaysMatchedNothingNotNoRecordsYet is this
// punch's DONE WHEN clause 1's contrast case: a project that DOES have
// records, queried with text that matches none of them, must not say "no
// records yet" (false — the project has history) and must instead say the
// query itself matched nothing.
//
// RA-MUTATION-PROBE: emptyResultMessage's `if strings.TrimSpace(query) ==
// ""` (mcp/recall.go) inverted to always take the "no records yet" branch
// -> RED (this test's assertion that Message names the query, not "no
// records yet", fails); restore -> GREEN. See this task's proof.
func TestRecallQueryMatchingNothingSaysMatchedNothingNotNoRecordsYet(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	noteText(t, shim, "decision", "chose approach A")

	args, err := json.Marshal(RecallParams{Query: "zzz-no-such-term-zzz"})
	if err != nil {
		t.Fatalf("marshal RecallParams: %v", err)
	}
	result := callRecall(t, shim, args)

	if len(result.Items) != 0 {
		t.Fatalf("Items = %+v, want empty (query should match nothing)", result.Items)
	}
	want := `query "zzz-no-such-term-zzz" matched nothing`
	if result.Message != want {
		t.Errorf("Message = %q, want %q", result.Message, want)
	}
	if strings.Contains(result.Message, "no records yet") {
		t.Errorf("Message = %q, wrongly claims no records yet even though the project has one", result.Message)
	}
}

// longRecallBody is long enough (with its embedded newline) that headline,
// summary and full altitude renderings differ sharply in size: headline
// keeps only the first line, summary truncates around 240 runes, full keeps
// all ~420.
const longRecallBody = "resume here" +
	"\nThe rest of this record's body pads it out well past the summary altitude's " +
	"per-item truncation length, and well past what a one-line headline keeps, so " +
	"that headline, summary and full altitude renderings of the same fixture end up " +
	"needing very different token budgets to include the same number of items — " +
	"exactly the signal this test's budget math depends on."

// TestRecallAltitudeChangesItemCountUnderFixedBudget is DONE WHEN clause
// 4's altitude mutation target: at one fixed token budget, a shorter
// altitude (whose per-item rendering is smaller) fits strictly more items
// than a taller one — proof that the altitude argument actually reaches
// recall.Build rather than being ignored.
//
// RA-MUTATION-PROBE: handleRecall's `altitude, altitudeUsed, err :=
// parseAltitude(p.Altitude)` (mcp/recall.go) with the result hardcoded to
// recall.AltitudeSummary regardless of p.Altitude -> RED (this test: the
// headline and full calls both fit as many items as summary does, so
// headlineCount > summaryCount > fullCount fails); restored -> GREEN. See
// this task's proof.
func TestRecallAltitudeChangesItemCountUnderFixedBudget(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	const numDecisions = 10
	for i := 0; i < numDecisions; i++ {
		noteText(t, shim, "decision", fmt.Sprintf("%s (item %d)", longRecallBody, i))
	}

	const fixedBudget = 200

	counts := map[string]int{}
	for _, altitude := range []string{"headline", "summary", "full"} {
		args, err := json.Marshal(RecallParams{Altitude: altitude, BudgetTokens: fixedBudget})
		if err != nil {
			t.Fatalf("marshal RecallParams: %v", err)
		}
		result := callRecall(t, shim, args)
		if result.Altitude != altitude {
			t.Errorf("Altitude = %q, want %q", result.Altitude, altitude)
		}
		counts[altitude] = len(result.Items)
	}

	t.Logf("item counts under a %d-token budget: headline=%d summary=%d full=%d",
		fixedBudget, counts["headline"], counts["summary"], counts["full"])

	if counts["headline"] <= counts["summary"] {
		t.Errorf("headline kept %d items, summary kept %d: want headline strictly more (altitude not wired)",
			counts["headline"], counts["summary"])
	}
	if counts["summary"] <= counts["full"] {
		t.Errorf("summary kept %d items, full kept %d: want summary strictly more (altitude not wired)",
			counts["summary"], counts["full"])
	}
}

// TestRecallDetailAliasEchoesRequestedAltitude: asking for "detail" reports
// altitude "detail", altitude_level "full", and the same items as "full".
func TestRecallDetailAliasEchoesRequestedAltitude(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)
	for i := 0; i < 4; i++ {
		noteText(t, shim, "decision", fmt.Sprintf("%s (item %d)", longRecallBody, i))
	}
	detail := callRecall(t, shim, json.RawMessage(`{"altitude":"detail"}`))
	full := callRecall(t, shim, json.RawMessage(`{"altitude":"full"}`))
	if detail.Altitude != "detail" || detail.AltitudeLevel != "full" {
		t.Errorf("detail: altitude=%q altitude_level=%q, want detail/full", detail.Altitude, detail.AltitudeLevel)
	}
	if full.Altitude != "full" || full.AltitudeLevel != "full" {
		t.Errorf("full: altitude=%q altitude_level=%q, want full/full", full.Altitude, full.AltitudeLevel)
	}
	if len(detail.Items) == 0 || !reflect.DeepEqual(detail.Items, full.Items) {
		t.Errorf("detail items %+v differ from full items %+v", detail.Items, full.Items)
	}
}

// TestRecallUnknownAltitudeIsInvalidParams confirms an altitude value
// outside headline/summary/full/detail is rejected rather than silently
// defaulted — a caller with a typo learns about it instead of getting a
// silently different narrative.
func TestRecallUnknownAltitudeIsInvalidParams(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	shim := dialShim(t, sockPath)

	args := json.RawMessage(`{"altitude":"panoramic"}`)
	_, rerr := shim.CallTool(ToolRecall, args)
	if rerr == nil {
		t.Fatal("CallTool(recall, altitude=panoramic) = nil error, want invalid-params")
	}
	if rerr.Code != CodeInvalidParams {
		t.Errorf("error code = %d, want %d (CodeInvalidParams)", rerr.Code, CodeInvalidParams)
	}
}

// TestToolsCallRecallResultContainsIdsTiersAndSupersededMark is DONE WHEN
// clause 1, driven the way a real MCP client drives it: through
// Server.Serve's full JSON-RPC tools/call envelope (a "spec-shaped
// client"), not the CallTool shortcut. At each altitude, the
// CallToolResult's text (the 62c9ebd5 shape: the tool's JSON payload
// carried as content[0].text) contains every fixture item's full id, its
// tier, and a `superseded → <id>` mark on the handoff a later handoff
// superseded.
func TestToolsCallRecallResultContainsIdsTiersAndSupersededMark(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
	s := NewServer(func() (net.Conn, error) { return net.Dial("unix", sockPath) })
	t.Cleanup(func() { _ = s.Close() })

	oldHandoffID := noteText(t, s, "handoff", "state: about to be superseded")
	newHandoffID := noteSuperseding(t, s, "handoff", "state: ready for review", oldHandoffID)
	decisionID := noteText(t, s, "decision", "chose approach A")

	for _, altitude := range []string{"headline", "summary", "full"} {
		t.Run(altitude, func(t *testing.T) {
			args, err := json.Marshal(RecallParams{Altitude: altitude, BudgetTokens: 100000})
			if err != nil {
				t.Fatalf("marshal RecallParams: %v", err)
			}
			resp := serveOneToolCall(t, s, ToolRecall, args)
			if resp.Error != nil {
				t.Fatalf("tools/call(recall) error: %+v", resp.Error)
			}
			result := requireCallToolResult(t, resp.Result)
			if result.IsError {
				t.Fatalf("tools/call(recall) isError = true, want false: %s", result.Content[0].Text)
			}
			text := result.Content[0].Text

			for _, id := range []string{oldHandoffID, newHandoffID, decisionID} {
				if !strings.Contains(text, id) {
					t.Errorf("content[0].text does not contain item id %q: %s", id, text)
				}
			}
			if !strings.Contains(text, string(store.TierAgentDeclared)) {
				t.Errorf("content[0].text does not contain tier %q: %s", store.TierAgentDeclared, text)
			}
			if !strings.Contains(text, "superseded → "+newHandoffID) {
				t.Errorf("content[0].text does not contain the superseded mark %q: %s",
					"superseded → "+newHandoffID, text)
			}

			var structured RecallResult
			if err := json.Unmarshal(result.StructuredContent, &structured); err != nil {
				t.Fatalf("unmarshal structuredContent: %v", err)
			}
			if structured.Altitude != altitude {
				t.Errorf("structuredContent.altitude = %q, want %q", structured.Altitude, altitude)
			}
		})
	}
}
