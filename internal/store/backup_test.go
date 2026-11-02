package store

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func seedBackupProject(t *testing.T, s *Store, key string) {
	t.Helper()
	if err := s.UpsertProject(Project{Key: key, Toplevel: "/tmp/" + key, FirstSeen: time.Now()}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
}

func insertBackupRecord(s *Store, key, text string) (string, error) {
	return s.InsertRecord(InsertRecordParams{
		Identity:   Identity{Kind: IdentityAgent, Actor: "seed"},
		Kind:       KindNote,
		Text:       text,
		ProjectKey: key,
	})
}

// TestBackupMidWriteIsConsistentAndComplete runs Backup while a second
// connection (as the daemon is, in production) keeps committing records and
// events. The snapshot must pass integrity_check and hold every record
// committed before the backup began. A plain copy of backstory.db misses
// those rows (they sit in the -wal), which is what this test guards against.
func TestBackupMidWriteIsConsistentAndComplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backstory.db")
	reader := mustOpen(t, path)
	writer := mustOpen(t, path)
	const key = "proj-a"
	seedBackupProject(t, writer, key)

	var before []string
	for i := 0; i < 150; i++ {
		id, err := insertBackupRecord(writer, key, "before backup")
		if err != nil {
			t.Fatalf("seed InsertRecord: %v", err)
		}
		before = append(before, id)
	}

	stop := make(chan struct{})
	started := make(chan struct{})
	var wg sync.WaitGroup
	var writerErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		first := true
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := insertBackupRecord(writer, key, "during backup"); err != nil {
				writerErr = err
				return
			}
			if _, err := writer.AppendEvent(Event{TS: time.Now(), Kind: "test", Source: "daemon", Payload: "{}"}); err != nil {
				writerErr = err
				return
			}
			if first {
				first = false
				close(started)
			}
		}
	}()
	<-started

	dest := filepath.Join(t.TempDir(), "backups", "snap.db")
	info, err := reader.Backup(dest, false)
	close(stop)
	wg.Wait()
	if writerErr != nil {
		t.Fatalf("concurrent writer: %v", writerErr)
	}
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if info.Records < len(before) {
		t.Fatalf("backup holds %d records, want at least %d", info.Records, len(before))
	}

	db, err := sql.Open("sqlite", "file:"+dest+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var verdict string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&verdict); err != nil || verdict != "ok" {
		t.Fatalf("integrity_check = %q, %v", verdict, err)
	}
	for _, id := range before {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM records WHERE id = ?`, id).Scan(&n); err != nil || n != 1 {
			t.Fatalf("record %s committed before the backup is missing from it (n=%d, err=%v)", id, n, err)
		}
	}
}

func TestBackupModesAndRefusesExistingWithoutOverwrite(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	seedBackupProject(t, s, "p")
	dest := filepath.Join(t.TempDir(), "new", "b.db")

	if _, err := s.Backup(dest, false); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	fi, err := os.Stat(dest)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, %v; want 0600", fi.Mode().Perm(), err)
	}
	di, err := os.Stat(filepath.Dir(dest))
	if err != nil || di.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v, %v; want 0700", di.Mode().Perm(), err)
	}
	if _, err := s.Backup(dest, false); !errors.Is(err, ErrBackupExists) {
		t.Fatalf("second Backup err = %v, want ErrBackupExists", err)
	}
	if _, err := s.Backup(dest, true); err != nil {
		t.Fatalf("overwrite Backup: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Dir(dest))
	if len(entries) != 1 {
		t.Fatalf("leftover files in backup dir: %v", entries)
	}
}

// TestVerifyBackupRejectsCorruptCopy feeds VerifyBackup a snapshot with
// garbled pages: it must error rather than report counts.
func TestVerifyBackupRejectsCorruptCopy(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	seedBackupProject(t, s, "p")
	for i := 0; i < 300; i++ {
		if _, err := insertBackupRecord(s, "p", strings.Repeat("x", 200)); err != nil {
			t.Fatal(err)
		}
	}
	dest := filepath.Join(t.TempDir(), "b.db")
	if _, err := s.Backup(dest, false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	// Trash one interior page: the file still opens and the count queries
	// still answer, so only integrity_check can tell it is damaged.
	const page = 4096
	if len(b) < 14*page {
		t.Fatalf("snapshot too small (%d bytes) to corrupt page 12", len(b))
	}
	for i := 12 * page; i < 13*page; i++ {
		b[i] = byte(i * 31)
	}
	if err := os.WriteFile(dest, b, 0o600); err != nil {
		t.Fatal(err)
	}
	if info, err := VerifyBackup(dest); err == nil {
		t.Fatalf("VerifyBackup accepted a corrupted copy: %+v", info)
	}
}
