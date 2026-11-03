package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

func seedBackupStore(t *testing.T, dbPath string) {
	t.Helper()
	seedDeletableRecord(t, dbPath, "proj-one", "first")
	seedDeletableRecord(t, dbPath, "proj-two", "second")
}

func backupEnv(t *testing.T) (data string, dbPath string) {
	t.Helper()
	data = t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	dbPath = filepath.Join(data, "backstory", "backstory.db")
	seedBackupStore(t, dbPath)
	return data, dbPath
}

func TestBackupDefaultPathModesAndJSON(t *testing.T) {
	data, _ := backupEnv(t)
	var out, errb bytes.Buffer
	if code := run([]string{"backup", "--json"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	var info store.BackupInfo
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatalf("json: %v: %s", err, out.String())
	}
	dir := filepath.Join(data, "backstory", "backups")
	if filepath.Dir(info.Path) != dir || !regexp.MustCompile(`^backstory-\d{8}T\d{6}Z\.db$`).MatchString(filepath.Base(info.Path)) {
		t.Fatalf("path = %s", info.Path)
	}
	di, _ := os.Stat(dir)
	fi, err := os.Stat(info.Path)
	if err != nil || di.Mode().Perm() != 0o700 || fi.Mode().Perm() != 0o600 {
		t.Fatalf("modes dir=%v file=%v err=%v", di.Mode().Perm(), fi.Mode().Perm(), err)
	}
	if info.Size != fi.Size() || info.Projects != 2 || info.Records != 2 || info.SchemaVersion < 1 {
		t.Fatalf("info = %+v (file size %d)", info, fi.Size())
	}
}

func TestBackupOutTextAndForce(t *testing.T) {
	_, dbPath := backupEnv(t)
	st, err := store.Open(dbPath, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := st.Version()
	_ = st.Close()

	dest := filepath.Join(t.TempDir(), "x", "mine.db")
	var out, errb bytes.Buffer
	if code := run([]string{"backup", "--out", dest}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	for _, line := range []string{"path: " + dest, "projects: 2", "sessions: 0", "records: 2", "schema version: " + strconv.Itoa(want)} {
		if !strings.Contains(out.String(), line+"\n") {
			t.Errorf("output lacks %q:\n%s", line, out.String())
		}
	}

	out.Reset()
	errb.Reset()
	if code := run([]string{"backup", "--out", dest}, strings.NewReader(""), &out, &errb); code == 0 || !strings.Contains(errb.String(), "--force") {
		t.Fatalf("second backup: exit %d, stderr %q; want refusal naming --force", code, errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := run([]string{"backup", "--out", dest, "--force"}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("--force exit %d: %s", code, errb.String())
	}
}

func TestBackupWithoutStoreFails(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	var out, errb bytes.Buffer
	if code := run([]string{"backup"}, strings.NewReader(""), &out, &errb); code == 0 {
		t.Fatalf("exit 0 with no store; stdout %q", out.String())
	}
}

// seedRecordAt writes one record whose ts is the given instant.
func seedRecordAt(t *testing.T, dbPath string, ts time.Time) {
	t.Helper()
	st, err := store.Open(dbPath, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if err := st.UpsertProject(store.Project{Key: "proj-ts", Toplevel: "/tmp/proj-ts", FirstSeen: ts}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertRecord(store.InsertRecordParams{
		Identity: store.Identity{Kind: store.IdentityAgent, Actor: "seed"}, Kind: store.KindNote,
		Text: "dated", ProjectKey: "proj-ts",
	}); err != nil {
		t.Fatal(err)
	}
	// The store stamps wall-clock time; pin it (the append-only trigger
	// is dropped on this throwaway copy to allow it).
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`DROP TRIGGER records_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE records SET ts = ?`, ts.UnixNano()); err != nil {
		t.Fatal(err)
	}
}

var longDigits = regexp.MustCompile(`\d{15,}`)

// TestBackupAndRestoreShowNewestAsDateNotNanoseconds: newest activity is a
// local YYYY-MM-DD HH:MM in both commands' text, never a nanosecond count;
// JSON carries RFC 3339 UTC and omits the field for an empty store.
func TestBackupAndRestoreShowNewestAsDateNotNanoseconds(t *testing.T) {
	t.Setenv("TZ", "America/New_York")
	time.Local = time.FixedZone("EST", -5*3600)
	t.Cleanup(func() { time.Local = time.UTC })
	instant := time.Date(2026, 11, 2, 21, 8, 32, 0, time.UTC) // 16:08 local

	f := newRestoreFixture(t)
	seedRecordAt(t, f.dbPath, instant)

	var out, errb bytes.Buffer
	if code := run([]string{"backup", "--out", filepath.Join(t.TempDir(), "t.db")}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("backup exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "newest activity: 2026-11-02 16:08") || longDigits.MatchString(out.String()) {
		t.Errorf("backup text:\n%s", out.String())
	}

	out.Reset()
	bak := filepath.Join(t.TempDir(), "j.db")
	if code := run([]string{"backup", "--json", "--out", bak}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("backup --json exit %d: %s", code, errb.String())
	}
	var raw map[string]any
	if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if raw["newest"] != "2026-11-02T21:08:32Z" {
		t.Errorf("json newest = %v, want 2026-11-02T21:08:32Z", raw["newest"])
	}

	code, rout, rerr := f.restore(bak, "--yes")
	if code != 0 {
		t.Fatalf("restore exit %d: %s", code, rerr)
	}
	if !strings.Contains(rout, "newest activity 2026-11-02 16:08") || longDigits.MatchString(rout) {
		t.Errorf("restore text:\n%s", rout)
	}

	// Empty store: omitted in JSON, "none" in restore text.
	empty := newRestoreFixture(t)
	seedBackupStoreEmpty(t, empty.dbPath)
	out.Reset()
	ebak := filepath.Join(t.TempDir(), "e.db")
	if code := run([]string{"backup", "--json", "--out", ebak}, strings.NewReader(""), &out, &errb); code != 0 {
		t.Fatalf("empty backup exit %d: %s", code, errb.String())
	}
	if strings.Contains(out.String(), `"newest"`) {
		t.Errorf("empty store json has newest: %s", out.String())
	}
	if _, rout, _ := empty.restore(ebak, "--yes"); !strings.Contains(rout, "newest activity none") {
		t.Errorf("empty restore text:\n%s", rout)
	}
}

func seedBackupStoreEmpty(t *testing.T, dbPath string) {
	t.Helper()
	st, err := store.Open(dbPath, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
}
