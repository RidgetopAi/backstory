package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/store"
)

// editEnv is testXDGEnv with VISUAL/EDITOR always set explicitly (to "" when
// the test does not want one), so results never depend on the invoking
// environment.
func editEnv(dataDir, visual, editor string) []string {
	return append(envWithout(testXDGEnv("XDG_DATA_HOME="+dataDir), "VISUAL", "EDITOR"),
		"VISUAL="+visual, "EDITOR="+editor)
}

// runEditCLI runs `backstory edit args...`. stdin nil means /dev/null, a
// character device, which isTerminal treats as an interactive session (the
// TTY simulation); a non-nil reader is a pipe (non-TTY).
func runEditCLI(t *testing.T, bin string, env []string, stdin *string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"edit"}, args...)...) //nolint:gosec // bin is the binary this test just built
	cmd.Env = env
	if stdin != nil {
		cmd.Stdin = strings.NewReader(*stdin)
	} else {
		f, err := os.Open(os.DevNull)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		cmd.Stdin = f
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	code = 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run edit: %v", err)
	}
	return out.String(), errOut.String(), code
}

func seedEditRecord(t *testing.T, dbPath, key, text string, about []string) string {
	t.Helper()
	st, err := store.Open(dbPath, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if err := st.UpsertProject(store.Project{Key: key, Toplevel: "/tmp/" + key}); err != nil {
		t.Fatal(err)
	}
	id, err := st.InsertRecord(store.InsertRecordParams{
		Identity:   store.Identity{Kind: store.IdentityAgent, Actor: "seed"},
		Kind:       store.KindDecision,
		Text:       text,
		About:      about,
		ProjectKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func recordCount(t *testing.T, dbPath, key string) int {
	t.Helper()
	st, err := store.Open(dbPath, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	recs, err := st.RecordsForProjectAll(key, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return len(recs)
}

func decodeEditRecords(t *testing.T, out string) []recordRowJSON {
	t.Helper()
	var o recordsOutputJSON
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("bad JSON %q: %v", out, err)
	}
	return o.Records
}

type editFixture struct {
	bin, dataDir, dbPath, key string
}

func newEditFixture(t *testing.T) editFixture {
	t.Helper()
	dataDir := t.TempDir()
	return editFixture{
		bin:     buildBackstory(t),
		dataDir: dataDir,
		dbPath:  filepath.Join(dataDir, "backstory", "backstory.db"),
		key:     "proj-edit",
	}
}

func TestEditStdinWritesSupersedingHumanRecord(t *testing.T) {
	f := newEditFixture(t)
	oldID := seedEditRecord(t, f.dbPath, f.key, "use A", []string{"internal/a.go"})
	before := mustGetRecord(t, f.dbPath, oldID)

	in := "use B instead\n"
	out, errOut, code := runEditCLI(t, f.bin, editEnv(f.dataDir, "", ""), &in, "--stdin", oldID[:8])
	if code != 0 {
		t.Fatalf("exit %d: %s%s", code, out, errOut)
	}
	if recordCount(t, f.dbPath, f.key) != 2 {
		t.Fatalf("want exactly one new record, got %d total", recordCount(t, f.dbPath, f.key))
	}
	if after := mustGetRecord(t, f.dbPath, oldID); !reflect.DeepEqual(before, after) {
		t.Fatalf("old record changed:\nbefore %+v\nafter  %+v", before, after)
	}

	st, err := store.Open(f.dbPath, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	edges, err := st.EdgesTouching(oldID)
	if err != nil || len(edges) != 1 {
		t.Fatalf("edges = %v, %v", edges, err)
	}
	e := edges[0]
	if e.Type != store.EdgeSupersedes || e.ToID != oldID {
		t.Fatalf("edge = %+v, want supersedes new→old", e)
	}
	nr := mustGetRecord(t, f.dbPath, e.FromID)
	if nr.Tier != store.TierHumanDeclared || nr.Text != "use B instead" || nr.Kind != before.Kind ||
		nr.ProjectKey != f.key || !reflect.DeepEqual(nr.About, before.About) || nr.SessionID != "" {
		t.Fatalf("new record = %+v", nr)
	}
	if want := "edited " + oldID + " → " + nr.ID + "\n"; out != want {
		t.Fatalf("stdout = %q, want %q", out, want)
	}

	// records --json: new current, old superseded_by new (with --history).
	rout, rerr, rcode := runRecordsCLI(t, f.dataDir, "--project", f.key, "--json", "--history")
	if rcode != 0 {
		t.Fatalf("records: %d %s", rcode, rerr)
	}
	status := map[string]recordRowJSON{}
	for _, r := range decodeEditRecords(t, rout) {
		status[r.ID] = r
	}
	if status[nr.ID].Status != "current" || status[oldID].Status != "superseded" || status[oldID].SupersededBy != nr.ID {
		t.Fatalf("records = %+v", status)
	}
	rout, _, _ = runRecordsCLI(t, f.dataDir, "--project", f.key, "--json")
	if rows := decodeEditRecords(t, rout); len(rows) != 1 || rows[0].ID != nr.ID {
		t.Fatalf("default records = %+v", rows)
	}
}

func TestEditFileSource(t *testing.T) {
	f := newEditFixture(t)
	oldID := seedEditRecord(t, f.dbPath, f.key, "use A", nil)
	p := filepath.Join(t.TempDir(), "new.txt")
	if err := os.WriteFile(p, []byte("from file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	in := ""
	out, errOut, code := runEditCLI(t, f.bin, editEnv(f.dataDir, "", ""), &in, oldID, "--file", p)
	if code != 0 || !strings.HasPrefix(out, "edited ") {
		t.Fatalf("exit %d: %s%s", code, out, errOut)
	}
}

// writeEditorScript writes a shell script that records its argument's
// original contents to <dir>/seen, then overwrites the file with rewrite.
func writeEditorScript(t *testing.T, name, rewrite string) (script, seen string) {
	t.Helper()
	dir := t.TempDir()
	seen = filepath.Join(dir, name+".seen")
	script = filepath.Join(dir, name)
	body := "#!/bin/sh\ncat \"$1\" > '" + seen + "'\nprintf '%s\\n' '" + rewrite + "' > \"$1\"\nstat -c %a \"$1\" > '" + seen + ".mode'\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil { //nolint:gosec // test script must be executable
		t.Fatal(err)
	}
	return script, seen
}

func TestEditEditorPrefilledAndVisualWins(t *testing.T) {
	f := newEditFixture(t)
	oldID := seedEditRecord(t, f.dbPath, f.key, "use A", nil)
	visual, visualSeen := writeEditorScript(t, "visual", "via visual")
	editor, editorSeen := writeEditorScript(t, "editor", "via editor")

	out, errOut, code := runEditCLI(t, f.bin, editEnv(f.dataDir, visual, editor), nil, oldID)
	if code != 0 {
		t.Fatalf("exit %d: %s%s", code, out, errOut)
	}
	if b, _ := os.ReadFile(visualSeen); strings.TrimSpace(string(b)) != "use A" { //nolint:gosec // test temp path
		t.Fatalf("editor saw %q, want prefilled old text", b)
	}
	if mode, _ := os.ReadFile(visualSeen + ".mode"); strings.TrimSpace(string(mode)) != "600" { //nolint:gosec // test temp path
		t.Fatalf("temp file mode = %q, want 600", mode)
	}
	if _, err := os.Stat(editorSeen); err == nil {
		t.Fatal("$EDITOR ran although $VISUAL was set")
	}
	newID := strings.TrimSpace(out[strings.Index(out, "→")+len("→"):])
	if got := mustGetRecord(t, f.dbPath, newID).Text; got != "via visual" {
		t.Fatalf("new text = %q", got)
	}

	// $EDITOR is used when VISUAL is empty.
	out, errOut, code = runEditCLI(t, f.bin, editEnv(f.dataDir, "", editor), nil, newID)
	if code != 0 {
		t.Fatalf("exit %d: %s%s", code, out, errOut)
	}
	if b, _ := os.ReadFile(editorSeen); strings.TrimSpace(string(b)) != "via visual" { //nolint:gosec // test temp path
		t.Fatalf("$EDITOR saw %q", b)
	}
}

func TestEditNoChange(t *testing.T) {
	f := newEditFixture(t)
	oldID := seedEditRecord(t, f.dbPath, f.key, "use A", nil)
	for name, in := range map[string]string{"unchanged": "use A\n", "empty": "", "whitespace": "  \n"} {
		out, errOut, code := runEditCLI(t, f.bin, editEnv(f.dataDir, "", ""), &in, "--stdin", oldID)
		if code != 0 || out != "no change\n" {
			t.Fatalf("%s: exit %d out %q err %q", name, code, out, errOut)
		}
		if n := recordCount(t, f.dbPath, f.key); n != 1 {
			t.Fatalf("%s: record count = %d, want 1", name, n)
		}
	}
}

func TestEditRefusesTombstonedAndSuperseded(t *testing.T) {
	f := newEditFixture(t)
	env := editEnv(f.dataDir, "", "")
	dead := seedEditRecord(t, f.dbPath, f.key, "dead", nil)
	old := seedEditRecord(t, f.dbPath, f.key, "old", nil)

	st, err := store.Open(f.dbPath, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.TombstoneRecord(dead, humanIdentity); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	in := "x\n"
	if _, errOut, code := runEditCLI(t, f.bin, env, &in, "--stdin", dead); code != 1 || !strings.Contains(errOut, "deleted") {
		t.Fatalf("tombstoned: exit %d %q", code, errOut)
	}

	out, _, code := runEditCLI(t, f.bin, env, &in, "--stdin", old)
	if code != 0 {
		t.Fatal("first edit failed")
	}
	head := strings.TrimSpace(out[strings.Index(out, "→")+len("→"):])
	in2 := "y\n"
	if _, errOut, code := runEditCLI(t, f.bin, env, &in2, "--stdin", old); code != 1 || !strings.Contains(errOut, head) {
		t.Fatalf("superseded: exit %d %q (want head %s)", code, errOut, head)
	}
	if n := recordCount(t, f.dbPath, f.key); n != 3 {
		t.Fatalf("record count = %d, want 3", n)
	}
}

func TestEditNonTTYWithoutSourceIsUsageError(t *testing.T) {
	f := newEditFixture(t)
	oldID := seedEditRecord(t, f.dbPath, f.key, "use A", nil)
	editor, seen := writeEditorScript(t, "editor", "nope")
	for name, env := range map[string][]string{
		"unset": editEnv(f.dataDir, "", ""),
		"set":   editEnv(f.dataDir, editor, editor),
	} {
		in := ""
		_, errOut, code := runEditCLI(t, f.bin, env, &in, oldID)
		if code != 2 || !strings.Contains(errOut, "usage") {
			t.Fatalf("%s: exit %d %q", name, code, errOut)
		}
	}
	if _, err := os.Stat(seen); err == nil {
		t.Fatal("editor ran on a non-TTY")
	}
}
