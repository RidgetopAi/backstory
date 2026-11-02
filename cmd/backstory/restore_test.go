package main

import (
	"bytes"
	"database/sql"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/store"
)

// restoreFixture puts a recording systemctl stub first on PATH (as
// ops/install_test.go's installFixture does) so no test touches the real
// service manager. Every unit reports active.
type restoreFixture struct {
	t      *testing.T
	log    string
	dbPath string
}

func newRestoreFixture(t *testing.T) *restoreFixture {
	t.Helper()
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	fakes := t.TempDir()
	log := filepath.Join(t.TempDir(), "calls.log")
	stub := "#!/bin/sh\necho \"systemctl $*\" >> \"" + log + "\"\nexit 0\n"
	if err := os.WriteFile(filepath.Join(fakes, "systemctl"), []byte(stub), 0o755); err != nil { //nolint:gosec // test stub
		t.Fatal(err)
	}
	t.Setenv("PATH", fakes+string(os.PathListSeparator)+os.Getenv("PATH"))
	return &restoreFixture{t: t, log: log, dbPath: filepath.Join(data, "backstory", "backstory.db")}
}

func (f *restoreFixture) calls() []string {
	b, _ := os.ReadFile(f.log) //nolint:gosec // test temp path
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if strings.HasPrefix(l, "systemctl --user stop ") || strings.HasPrefix(l, "systemctl --user start ") {
			out = append(out, strings.TrimPrefix(l, "systemctl --user "))
		}
	}
	return out
}

func (f *restoreFixture) count() store.BackupInfo {
	f.t.Helper()
	info, err := store.VerifyBackup(f.dbPath)
	if err != nil {
		f.t.Fatal(err)
	}
	return info
}

func (f *restoreFixture) restore(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(append([]string{"restore"}, args...), strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

func (f *restoreFixture) backup(name string) string {
	f.t.Helper()
	dest := filepath.Join(f.t.TempDir(), name)
	var out, errb bytes.Buffer
	if code := run([]string{"backup", "--out", dest}, strings.NewReader(""), &out, &errb); code != 0 {
		f.t.Fatalf("backup: %s", errb.String())
	}
	return dest
}

func (f *restoreFixture) preRestore() []string {
	m, _ := filepath.Glob(f.dbPath + ".pre-restore-*")
	return m
}

func sameCounts(a, b store.BackupInfo) bool {
	return a.Projects == b.Projects && a.Sessions == b.Sessions && a.Records == b.Records
}

func TestRestoreRoundTrip(t *testing.T) {
	f := newRestoreFixture(t)
	seedBackupStore(t, f.dbPath)
	bak := f.backup("b.db")
	want := f.count()

	seedDeletableRecord(t, f.dbPath, "proj-three", "later")
	seedDeletableRecord(t, f.dbPath, "proj-one", "more")
	before := f.count()
	if sameCounts(before, want) {
		t.Fatal("fixture did not change the store")
	}

	code, out, errs := f.restore(bak, "--yes")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errs)
	}
	if got := f.count(); !sameCounts(got, want) {
		t.Fatalf("restored counts %+v; want %+v", got, want)
	}
	kept := f.preRestore()
	if len(kept) != 1 {
		t.Fatalf("pre-restore copies = %v", kept)
	}
	pre, err := store.VerifyBackup(kept[0])
	if err != nil || !sameCounts(pre, before) {
		t.Fatalf("pre-restore copy %+v (%v); want counts of %+v", pre, err, before)
	}
	if !strings.Contains(out, kept[0]) || !strings.Contains(out, "running again") {
		t.Errorf("output lacks kept path / daemon note:\n%s", out)
	}
	stops := strings.Join(f.calls(), ",")
	if !strings.Contains(stops, "stop backstory.service") || !strings.Contains(stops, "start backstory.service") {
		t.Errorf("service calls = %s", stops)
	}
}

func TestRestoreRefusalsChangeNothing(t *testing.T) {
	f := newRestoreFixture(t)
	seedBackupStore(t, f.dbPath)
	good := f.backup("good.db")

	dir := t.TempDir()
	newer := filepath.Join(dir, "newer.db")
	b, _ := os.ReadFile(good)                             //nolint:gosec // test temp path
	if err := os.WriteFile(newer, b, 0o600); err != nil { //nolint:gosec // test temp path
		t.Fatal(err)
	}
	st, err := store.Open(newer, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	bumpSchema(t, newer, store.SchemaVersion+1)

	corrupt := filepath.Join(dir, "corrupt.db")
	seedBig := filepath.Join(dir, "big.db")
	for i := 0; i < 300; i++ {
		seedDeletableRecord(t, seedBig, "p", strings.Repeat("x", 200))
	}
	cb, _ := os.ReadFile(seedBig) //nolint:gosec // test temp path
	if len(cb) < 14*4096 {
		// the live db may sit in the WAL; snapshot through the store
		st, err := store.Open(seedBig, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.Backup(filepath.Join(dir, "big-snap.db"), false); err != nil {
			t.Fatal(err)
		}
		_ = st.Close()
		cb, _ = os.ReadFile(filepath.Join(dir, "big-snap.db")) //nolint:gosec // test temp path
	}
	for i := 12 * 4096; i < 13*4096; i++ {
		cb[i] = byte(i % 251) //nolint:gosec // reduced mod 251
	}
	if err := os.WriteFile(corrupt, cb, 0o600); err != nil { //nolint:gosec // test temp path
		t.Fatal(err)
	}

	notdb := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notdb, []byte(strings.Repeat("not a database ", 200)), 0o600); err != nil {
		t.Fatal(err)
	}

	liveBytes, _ := os.ReadFile(f.dbPath) //nolint:gosec // test temp path
	liveCounts := f.count()
	cases := []struct {
		name string
		args []string
		msg  string
	}{
		{"newer schema", []string{newer, "--yes"}, "newer Backstory"},
		{"corrupt", []string{corrupt, "--yes"}, "not a usable"},
		{"non-sqlite", []string{notdb, "--yes"}, "not a usable"},
		{"missing", []string{filepath.Join(dir, "nope.db"), "--yes"}, "not a readable"},
		{"no tty no yes", []string{good}, "--yes"},
	}
	for _, c := range cases {
		code, _, errs := f.restore(c.args...)
		if code == 0 || !strings.Contains(errs, c.msg) {
			t.Errorf("%s: exit %d, stderr %q; want non-zero naming %q", c.name, code, errs, c.msg)
		}
	}
	after, _ := os.ReadFile(f.dbPath) //nolint:gosec // test temp path
	if !bytes.Equal(after, liveBytes) || !sameCounts(f.count(), liveCounts) {
		t.Error("a refused restore changed the live store")
	}
	if c := f.calls(); len(c) != 0 {
		t.Errorf("service manager was touched: %v", c)
	}
	if k := f.preRestore(); len(k) != 0 {
		t.Errorf("pre-restore copies after refusals: %v", k)
	}
}

func bumpSchema(t *testing.T, path string, v int) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`INSERT INTO schema_version (version, applied_at) VALUES (?, 0)`, v); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreFailureAfterStopRollsBack(t *testing.T) {
	for _, step := range []string{"rename", "open"} {
		t.Run(step, func(t *testing.T) {
			f := newRestoreFixture(t)
			seedBackupStore(t, f.dbPath)
			bak := f.backup("b.db")
			seedDeletableRecord(t, f.dbPath, "proj-extra", "extra")
			want := f.count()

			restoreFailHook = func(s string) error {
				if s == step {
					return errors.New("injected " + step + " failure")
				}
				return nil
			}
			t.Cleanup(func() { restoreFailHook = nil })

			code, _, errs := f.restore(bak, "--yes")
			if code == 0 || !strings.Contains(errs, "injected "+step) {
				t.Fatalf("exit %d, stderr %q", code, errs)
			}
			if got := f.count(); !sameCounts(got, want) {
				t.Fatalf("after rollback counts %+v; want %+v", got, want)
			}
			calls := strings.Join(f.calls(), ",")
			si, ti := strings.Index(calls, "stop "), strings.LastIndex(calls, "start ")
			if si < 0 || ti < si {
				t.Errorf("want a stop then a start, got %s", calls)
			}
		})
	}
}

func TestRestoreRefusesLiveDaemonWithoutSystemd(t *testing.T) {
	f := newRestoreFixture(t)
	seedBackupStore(t, f.dbPath)
	bak := f.backup("b.db")
	// no systemctl at all: an empty PATH dir
	t.Setenv("PATH", t.TempDir())
	sock, err := socketPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	code, _, errs := f.restore(bak, "--yes")
	if code == 0 || !strings.Contains(errs, "daemon is running") {
		t.Fatalf("exit %d, stderr %q", code, errs)
	}
	if k := f.preRestore(); len(k) != 0 {
		t.Errorf("pre-restore copy made despite refusal: %v", k)
	}
}
