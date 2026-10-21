package store

import (
	"path/filepath"
	"testing"
)

func countSelf(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM devices WHERE is_self = 1`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestOpenMintsOneStableSelfDevice (task 4f00e324): a fresh store has exactly
// one self device; reopening keeps its id.
func TestOpenMintsOneStableSelfDevice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d.db")
	s := mustOpen(t, path)
	if n := countSelf(t, s); n != 1 {
		t.Fatalf("self devices = %d, want 1", n)
	}
	d1, err := s.SelfDevice()
	if err != nil || d1.ID == "" || d1.Name == "" || !d1.IsSelf {
		t.Fatalf("SelfDevice = %+v, %v", d1, err)
	}
	_ = s.Close()
	s2 := mustOpen(t, path)
	d2, err := s2.SelfDevice()
	if err != nil || d2.ID != d1.ID {
		t.Fatalf("reopened id = %q, %v; want %q", d2.ID, err, d1.ID)
	}
	if n := countSelf(t, s2); n != 1 {
		t.Fatalf("self devices after reopen = %d, want 1", n)
	}
}

// TestMigrationV16KeepsRowsWithNullOrigin: a pre-v10 store (migrated through v16) gains a self device and
// keeps every record and session with origin_device NULL.
func TestMigrationV16KeepsRowsWithNullOrigin(t *testing.T) {
	if SchemaVersion < 16 {
		t.Fatalf("SchemaVersion = %d, want >= 16", SchemaVersion)
	}
	raw := filepath.Join(t.TempDir(), "v9.db")
	db := buildFixtureThroughVersion(t, raw, 9)
	if _, err := db.Exec(`INSERT INTO sessions (id, agent, cwd, started_at, origin) VALUES ('s1','claude','/p',1,'live')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO records (id, ts, kind, tier, text, about, evidence) VALUES ('r1',1,'note','agent-declared','t','a','[]')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s := mustOpen(t, raw)
	if n := countSelf(t, s); n != 1 {
		t.Fatalf("self devices = %d, want 1", n)
	}
	for _, q := range []string{
		`SELECT COUNT(*) FROM records WHERE id='r1' AND origin_device IS NULL`,
		`SELECT COUNT(*) FROM sessions WHERE id='s1' AND origin_device IS NULL`,
	} {
		var n int
		if err := s.DB().QueryRow(q).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s = %d, %v; want 1", q, n, err)
		}
	}
}

// TestRecordsNoUpdateRefusesOriginDevice: invariant 1 covers origin_device.
func TestRecordsNoUpdateRefusesOriginDevice(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "t.db"))
	if _, err := s.DB().Exec(`INSERT INTO records (id, ts, kind, tier, text, about, evidence) VALUES ('r1',1,'note','agent-declared','t','a','[]')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`UPDATE records SET origin_device = 'other' WHERE id = 'r1'`); err == nil {
		t.Fatal("UPDATE of records.origin_device succeeded, want it refused by records_no_update")
	}
}
