package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/recall"
	"github.com/RidgetopAi/backstory/internal/store"
)

const (
	recNoteID      = "aaaa0001-0000-4000-8000-000000000001" // current
	recOldDecID    = "aaaa0002-0000-4000-8000-000000000002" // superseded
	recClaimID     = "aaaa0003-0000-4000-8000-000000000003" // expired
	recHandoffID   = "aaaa0004-0000-4000-8000-000000000004" // possibly-stale
	recDeletedID   = "aaaa0005-0000-4000-8000-000000000005" // tombstoned
	recNewDecID    = "aaaa0006-0000-4000-8000-000000000006" // supersedes recOldDecID
	recContradicID = "aaaa0007-0000-4000-8000-000000000007" // contradicts the handoff
)

// buildRecordsFixtureStore seeds one project with a current note, a
// superseded decision, an expired claim, a possibly-stale handoff and a
// tombstoned note (plus the superseding decision and the contradicting
// note that make two of those states true), in ascending insert order.
func buildRecordsFixtureStore(t *testing.T, dataDir string) {
	t.Helper()
	st, err := store.Open(filepath.Join(dataDir, "backstory", "backstory.db"), nil, nil)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() { _ = st.Close() }()
	if err := st.UpsertProject(store.Project{Key: recallFixtureProject, Toplevel: recallFixtureProject, FirstSeen: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ins := func(i int, id string, kind store.RecordKind, tier store.Tier, text string) {
		mustInsertFixtureRecord(t, st, id, base.Add(time.Duration(i)*time.Minute), kind, tier, text)
	}
	ins(0, recNoteID, store.KindNote, store.TierAgentDeclared, "A current note.\nSecond line.")
	ins(1, recOldDecID, store.KindDecision, store.TierHumanDeclared, "Use option A.")
	if _, err := st.DB().Exec(`INSERT INTO records
		(id, ts, kind, tier, text, about, session_id, project_key, evidence, outcome, promoter, expires_at, tombstoned_at)
		VALUES (?, ?, ?, ?, ?, '[]', NULL, ?, '[]', NULL, NULL, ?, NULL)`,
		recClaimID, base.Add(2*time.Minute).UnixNano(), string(store.KindClaim), string(store.TierAgentDeclared),
		"Build is green.", recallFixtureProject, base.Add(time.Hour).UnixNano()); err != nil {
		t.Fatal(err)
	}
	ins(3, recHandoffID, store.KindHandoff, store.TierAgentDeclared, "Resume here.")
	ins(4, recDeletedID, store.KindNote, store.TierAgentDeclared, "secret note to delete")
	ins(5, recNewDecID, store.KindDecision, store.TierHumanDeclared, "Use option B.")
	ins(6, recContradicID, store.KindNote, store.TierAgentDeclared, "The handoff is wrong.")
	if err := st.LinkEdge(recNewDecID, recOldDecID, store.EdgeSupersedes, "human-cli-fixture"); err != nil {
		t.Fatal(err)
	}
	if err := st.LinkEdge(recContradicID, recHandoffID, store.EdgeContradicts, "human-cli-fixture"); err != nil {
		t.Fatal(err)
	}
	if err := st.TombstoneRecord(recDeletedID, store.Identity{Kind: store.IdentityHuman, Actor: "human"}); err != nil {
		t.Fatal(err)
	}
}

func runRecordsCLI(t *testing.T, dataDir string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", dataDir)
	var outBuf, errBuf bytes.Buffer
	code = run(append([]string{"records"}, args...), bytes.NewReader(nil), &outBuf, &errBuf)
	return outBuf.String(), errBuf.String(), code
}

func decodeRecords(t *testing.T, out string) []recordRowJSON {
	t.Helper()
	var o recordsOutputJSON
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("bad JSON %q: %v", out, err)
	}
	if o.ProjectKey != recallFixtureProject {
		t.Fatalf("project_key = %q", o.ProjectKey)
	}
	return o.Records
}

func TestRecordsGoldenHistory(t *testing.T) {
	dir := t.TempDir()
	buildRecordsFixtureStore(t, dir)
	out, errOut, code := runRecordsCLI(t, dir, "--project", recallFixtureProject, "--history", "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	compareGolden(t, "../records/history.json.golden", out)

	byID := map[string]recordRowJSON{}
	var order []string
	for _, r := range decodeRecords(t, out) {
		byID[r.ID] = r
		order = append(order, r.ID)
	}
	wantOrder := []string{recContradicID, recNewDecID, recDeletedID, recHandoffID, recClaimID, recOldDecID, recNoteID}
	if strings.Join(order, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("order = %v, want newest first %v", order, wantOrder)
	}
	for id, want := range map[string]string{
		recNoteID: "current", recOldDecID: "superseded", recClaimID: "expired",
		recHandoffID: "possibly-stale", recDeletedID: "deleted", recNewDecID: "current",
	} {
		if byID[id].Status != want {
			t.Errorf("%s status = %q, want %q", id, byID[id].Status, want)
		}
	}
	// clause 2
	if byID[recOldDecID].SupersededBy != recNewDecID {
		t.Errorf("superseded_by = %q, want %q", byID[recOldDecID].SupersededBy, recNewDecID)
	}
	if byID[recDeletedID].Text != "" {
		t.Errorf("deleted text = %q, want empty", byID[recDeletedID].Text)
	}
	if !strings.Contains(out, `"text":""`) {
		t.Errorf("deleted record's empty text must be present in JSON: %s", out)
	}
}

func TestRecordsGoldenDefaultHidesSupersededAndDeleted(t *testing.T) {
	dir := t.TempDir()
	buildRecordsFixtureStore(t, dir)
	out, errOut, code := runRecordsCLI(t, dir, "--project", recallFixtureProject, "--json")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	compareGolden(t, "../records/default.json.golden", out)
	for _, r := range decodeRecords(t, out) {
		if r.Status == "superseded" || r.Status == "deleted" {
			t.Errorf("default output contains %s record %s", r.Status, r.ID)
		}
	}
	if n := len(decodeRecords(t, out)); n != 5 {
		t.Errorf("default record count = %d, want 5", n)
	}
}

// clause 3: records status == the recall engine's status for every record.
func TestRecordsStatusMatchesRecall(t *testing.T) {
	dir := t.TempDir()
	buildRecordsFixtureStore(t, dir)
	out, _, code := runRecordsCLI(t, dir, "--project", recallFixtureProject, "--history", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	st, err := store.Open(filepath.Join(dir, "backstory", "backstory.db"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	res, err := recall.Build(st, recall.ProjectAnchor(recallFixtureProject), recall.AltitudeHeadline, 1_000_000, nil)
	if err != nil {
		t.Fatal(err)
	}
	recallStatus := map[string]string{}
	for _, it := range res.Items {
		s := string(it.Status)
		if it.Status == recall.StatusTombstoned {
			s = "deleted"
		}
		recallStatus[it.ID] = s
	}
	rows := decodeRecords(t, out)
	if len(recallStatus) != len(rows) {
		t.Fatalf("recall has %d items, records %d", len(recallStatus), len(rows))
	}
	for _, r := range rows {
		if recallStatus[r.ID] != r.Status {
			t.Errorf("%s: records status %q != recall status %q", r.ID, r.Status, recallStatus[r.ID])
		}
	}
}

func TestRecordsKindFilterAndText(t *testing.T) {
	dir := t.TempDir()
	buildRecordsFixtureStore(t, dir)
	out, _, code := runRecordsCLI(t, dir, "--project", recallFixtureProject, "--kind", "note")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 note lines (deleted hidden), got %q", out)
	}
	if !strings.HasPrefix(lines[1], "aaaa0001  ") || !strings.Contains(lines[1], "note  agent-declared  current  A current note.") || strings.Contains(lines[1], "Second line") {
		t.Errorf("bad text line %q", lines[1])
	}
}

// clause 5.
func TestRecordsUnknownProjectAndHere(t *testing.T) {
	dir := t.TempDir()
	buildRecordsFixtureStore(t, dir)
	_, errOut, code := runRecordsCLI(t, dir, "--project", "no-such-project")
	if code == 0 || !strings.Contains(errOut, "no-such-project") {
		t.Fatalf("unknown project: code %d stderr %q", code, errOut)
	}

	cwd := t.TempDir()
	t.Chdir(cwd)
	want, err := resolveHumanProjectKey("")
	if err != nil {
		t.Fatal(err)
	}
	_, errOut, code = runRecordsCLI(t, dir, "--here")
	if code == 0 || !strings.Contains(errOut, want) {
		t.Fatalf("--here: code %d stderr %q, want it to name resolved key %q", code, errOut, want)
	}
	st, err := store.Open(filepath.Join(dir, "backstory", "backstory.db"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertProject(store.Project{Key: want, Toplevel: cwd, FirstSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	out, errOut, code := runRecordsCLI(t, dir, "--here", "--json")
	if code != 0 {
		t.Fatalf("--here on known project: %d %s", code, errOut)
	}
	if !strings.Contains(out, `"project_key":"`+want+`"`) || !strings.Contains(out, `"records":[]`) {
		t.Errorf("out = %s", out)
	}
}
