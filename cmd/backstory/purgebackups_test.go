package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

const purgeMarker = "MARKER-7f3a9c-do-not-keep"

type purgeBackupFixture struct {
	f         purgeFixture
	dbPath    string
	backupDir string
	storeDir  string
	auto      []string // backups in backupDir
	manual    string
	preCopy   string // in the store dir
	preRehome string // the handoff re-home copy, in the store dir
	outFile   string // unrelated dir
	notes     string
	link      string
	outside   string // symlink target, outside backupDir
}

func fileHas(t *testing.T, path, s string) bool {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // test reads a backup it just wrote
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Contains(b, []byte(s))
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// seedPurgeBackups writes a session holding purgeMarker, then every kind of
// backup around it.
func seedPurgeBackups(t *testing.T) purgeBackupFixture {
	t.Helper()
	data, dbPath := backupEnv(t)
	st, err := openStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if err := st.UpsertProject(store.Project{Key: purgeProject, Toplevel: purgeProject, FirstSeen: autoT0}); err != nil {
		t.Fatal(err)
	}
	sid, err := st.StartSession(store.StartSessionParams{Agent: "claude", HarnessSessionID: "h-marker", CWD: purgeProject, ProjectKey: purgeProject, StartedAt: autoT0, Origin: store.OriginBackfilled})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendEvent(store.Event{TS: autoT0, Kind: "tool.use", SessionID: sid, Source: "backfill", Payload: `{"text":"` + purgeMarker + `"}`}); err != nil {
		t.Fatal(err)
	}
	fx := purgeBackupFixture{dbPath: dbPath, backupDir: filepath.Join(data, "backstory", "backups"), storeDir: filepath.Dir(dbPath)}
	fx.f.a = sid
	mk := func(dest string) string {
		if _, err := st.Backup(dest, false); err != nil {
			t.Fatal(err)
		}
		if !fileHas(t, dest, purgeMarker) {
			t.Fatalf("%s should hold the marker", dest)
		}
		return dest
	}
	for _, d := range []time.Duration{0, 24 * time.Hour} {
		fx.auto = append(fx.auto, mk(filepath.Join(fx.backupDir, store.AutoBackupName(autoT0.Add(d)))))
	}
	fx.manual = mk(filepath.Join(fx.backupDir, "backstory-20260302T090000Z.db"))
	fx.preCopy = mk(dbPath + ".pre-restore-20260303T090000Z")
	fx.preRehome = mk(dbPath + ".pre-rehome-20260303T090000.123456789Z")
	fx.outFile = mk(filepath.Join(t.TempDir(), "mine.db"))

	fx.notes = filepath.Join(fx.backupDir, "notes.txt")
	if err := os.WriteFile(fx.notes, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	fx.outside = filepath.Join(t.TempDir(), "outside.db")
	if err := os.WriteFile(fx.outside, []byte("outside "+purgeMarker), 0o600); err != nil {
		t.Fatal(err)
	}
	fx.link = filepath.Join(fx.backupDir, "backstory-20260304T090000Z.db")
	if err := os.Symlink(fx.outside, fx.link); err != nil {
		t.Fatal(err)
	}
	return fx
}

// snapshot is name -> size+mtime for every entry in dir, for "nothing changed".
func snapshot(t *testing.T, dirs ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, d := range dirs {
		ents, err := os.ReadDir(d)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range ents {
			fi, err := os.Lstat(filepath.Join(d, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			out[filepath.Join(d, e.Name())] = fi.ModTime().String() + "/" + strconv.FormatInt(fi.Size(), 10)
		}
	}
	return out
}

func TestPurgeDeletesBackupsHoldingTextThenTakesFreshOne(t *testing.T) {
	fx := seedPurgeBackups(t)
	code, out, errs := runAutoCLI(t, "purge", "--session", fx.f.a, "--yes")
	if code != 0 {
		t.Fatalf("exit %d out=%s err=%s", code, out, errs)
	}
	if n := countRows(t, fx.dbPath, `SELECT COUNT(*) FROM timeline_events WHERE payload LIKE ?`, "%"+purgeMarker+"%"); n != 0 {
		t.Errorf("store still has the marker in %d events", n)
	}
	for _, p := range append(append([]string{}, fx.auto...), fx.manual, fx.preCopy, fx.preRehome) {
		if exists(p) {
			t.Errorf("%s survived the purge", p)
		}
	}
	if !fileHas(t, fx.outFile, purgeMarker) {
		t.Errorf("--out file was touched")
	}
	if !strings.Contains(out, purgeBackupsWrittenElsewhere) {
		t.Errorf("no written-elsewhere note: %s", out)
	}
	// exactly one new backup in backupDir, without the marker
	var fresh []string
	ents, err := os.ReadDir(fx.backupDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if backupFileName.MatchString(e.Name()) && e.Name() != filepath.Base(fx.link) {
			fresh = append(fresh, filepath.Join(fx.backupDir, e.Name()))
		}
	}
	if len(fresh) != 1 {
		t.Fatalf("new backups = %v, want exactly one", fresh)
	}
	if fileHas(t, fresh[0], purgeMarker) {
		t.Errorf("fresh backup holds the marker")
	}
	if _, err := store.VerifyBackup(fresh[0]); err != nil {
		t.Errorf("fresh backup invalid: %v", err)
	}
}

func TestPurgeDryRunListsBackupsAndDeletesNothing(t *testing.T) {
	fx := seedPurgeBackups(t)
	before := snapshot(t, fx.backupDir, fx.storeDir)
	code, out, errs := runAutoCLI(t, "purge", "--session", fx.f.a, "--dry-run")
	if code != 0 {
		t.Fatalf("exit %d err=%s", code, errs)
	}
	for _, p := range append(append([]string{}, fx.auto...), fx.manual, fx.preCopy, fx.preRehome) {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		line := ""
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, p) {
				line = l
			}
		}
		if line == "" {
			t.Errorf("dry-run does not list %s:\n%s", p, out)
			continue
		}
		if !strings.Contains(line, fi.ModTime().Local().Format("2006-01-02 15:04")) || !strings.Contains(line, " "+strconv.FormatInt(fi.Size(), 10)+" bytes") {
			t.Errorf("line lacks date/size: %q", line)
		}
	}
	for _, p := range []string{fx.link, fx.notes, fx.outFile} {
		if strings.Contains(out, p) {
			t.Errorf("dry-run lists %s", p)
		}
	}
	if !strings.Contains(out, purgeBackupsWrittenElsewhere) {
		t.Errorf("no written-elsewhere note:\n%s", out)
	}
	after := snapshot(t, fx.backupDir, fx.storeDir)
	for p, v := range before {
		if strings.HasPrefix(filepath.Base(p), "backstory.db") && !strings.Contains(p, ".pre-") {
			continue // the live store and its sidecars are checked by content below
		}
		if after[p] != v {
			t.Errorf("%s changed: %q -> %q", p, v, after[p])
		}
	}
	if len(after) < len(before) {
		t.Errorf("files removed: before %d after %d", len(before), len(after))
	}
	if n := countRows(t, fx.dbPath, `SELECT COUNT(*) FROM timeline_events WHERE payload LIKE ?`, "%"+purgeMarker+"%"); n != 1 {
		t.Errorf("dry-run changed the store (marker events = %d)", n)
	}
}

func TestPurgeLeavesSymlinksAndUnrelatedFiles(t *testing.T) {
	fx := seedPurgeBackups(t)
	if code, out, errs := runAutoCLI(t, "purge", "--session", fx.f.a, "--yes"); code != 0 {
		t.Fatalf("exit %d out=%s err=%s", code, out, errs)
	}
	if fi, err := os.Lstat(fx.link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("symlink gone or replaced: %v", err)
	}
	if !fileHas(t, fx.outside, purgeMarker) {
		t.Errorf("symlink target was touched")
	}
	if !fileHas(t, fx.notes, "keep me") {
		t.Errorf("notes.txt was touched")
	}
}

func TestPurgeEraseFailureKeepsEveryBackup(t *testing.T) {
	fx := seedPurgeBackups(t)
	purgeEraseHook = func() error { return os.ErrPermission }
	t.Cleanup(func() { purgeEraseHook = nil })
	before := snapshot(t, fx.backupDir, fx.storeDir)
	code, _, _ := runAutoCLI(t, "purge", "--session", fx.f.a, "--yes")
	if code == 0 {
		t.Fatalf("purge succeeded despite failed erase")
	}
	after := snapshot(t, fx.backupDir, fx.storeDir)
	for p := range before {
		if _, ok := after[p]; !ok {
			t.Errorf("%s was removed", p)
		}
	}
	if len(after) != len(before) {
		t.Errorf("a backup was written despite the failed erase")
	}
}
