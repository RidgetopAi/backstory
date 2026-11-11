package store

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func modeOf(t *testing.T, p string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat %s: %v", p, err)
	}
	return fi.Mode().Perm()
}

func oneMatch(t *testing.T, pattern string) string {
	t.Helper()
	m, err := filepath.Glob(pattern)
	if err != nil || len(m) != 1 {
		t.Fatalf("glob %s = %v (%v), want exactly 1", pattern, m, err)
	}
	return m[0]
}

// Under umask 0022 VACUUM INTO would write 0644; every store-side safety
// copy must still be BackupFileMode.
func TestStoreSideBackupsAre0600UnderUmask0022(t *testing.T) {
	old := syscall.Umask(0o022)
	defer syscall.Umask(old)

	t.Run("rehome", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "backstory.db")
		ws := "workspace:" + filepath.Join(dir, "projects")
		repo := filepath.Join(dir, "projects", "tide")
		s, err := Open(path, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		db := s.DB()
		rawInsertProject(t, db, ws, ws, sweepT1)
		rawInsertProject(t, db, repo, repo, sweepT1)
		rawInsertSession(t, db, "s-repo", repo, repo, sweepT1)
		rawInsertRecord(t, db, "h-a", ws, "s-repo", KindHandoff, sweepT2)
		_ = s.Close()
		s, err = Open(path, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = s.Close()
		if got := modeOf(t, oneMatch(t, path+".pre-rehome-*")); got != BackupFileMode {
			t.Errorf("pre-rehome mode = %o, want %o", got, BackupFileMode)
		}
	})

	t.Run("sweep", func(t *testing.T) {
		dbPath, w := seedLegacySweepFixture(t)
		s, err := Open(dbPath, []string{w}, fakeSweepGit{})
		if err != nil {
			t.Fatal(err)
		}
		_ = s.Close()
		if got := modeOf(t, oneMatch(t, dbPath+".pre-sweep-*")); got != BackupFileMode {
			t.Errorf("pre-sweep mode = %o, want %o", got, BackupFileMode)
		}
	})

	t.Run("identity", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "backstory.db")
		s, err := Open(path, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		key, common := filepath.Join(dir, "repo"), filepath.Join(dir, "repo", ".git")
		rawInsertProject(t, s.DB(), key, key, sweepT1)
		if _, err := s.DB().Exec(`UPDATE projects SET git_common_dir = ? WHERE key = ?`, common, key); err != nil {
			t.Fatal(err)
		}
		_ = s.Close()
		s, err = Open(path, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = s.Close()
		if got := modeOf(t, oneMatch(t, path+".pre-identity-*")); got != BackupFileMode {
			t.Errorf("pre-identity mode = %o, want %o", got, BackupFileMode)
		}
	})
}

func TestOpenTightensExistingPreCopies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "backstory.db")
	var loose []string
	for _, n := range []string{".pre-rehome-X", ".pre-sweep-X", ".pre-identity-X", ".pre-restore-X"} {
		p := path + n
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, 0o644); err != nil { //nolint:gosec // fixture: a legacy world-readable copy
			t.Fatal(err)
		}
		loose = append(loose, p)
	}
	s, err := Open(path, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	for _, p := range loose {
		if got := modeOf(t, p); got != BackupFileMode {
			t.Errorf("%s mode = %o after Open, want %o", filepath.Base(p), got, BackupFileMode)
		}
	}
}
