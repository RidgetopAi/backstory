package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

var autoT0 = time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)

func autoFiles(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

func countAuto(t *testing.T, dir string) int {
	t.Helper()
	fs, err := store.ListAutoBackups(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(fs)
}

func openBackupEnvStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	data, dbPath := backupEnv(t)
	st, err := openStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, filepath.Join(data, "backstory", "backups")
}

func runAutoCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

// Fake clock: start writes one, hourly checks within 24h write none, one more
// after 24h, nine days leave the newest 7, and a manual backup is untouched.
func TestAutoBackupDailyKeepSevenManualUntouched(t *testing.T) {
	st, dir := openBackupEnvStore(t)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	manual := filepath.Join(dir, "backstory-20250101T000000Z.db")
	if err := os.WriteFile(manual, []byte("manual"), 0o600); err != nil {
		t.Fatal(err)
	}
	def, _ := defaultBackupDir()

	ctx, cancel := context.WithCancel(context.Background())
	tick := make(chan time.Time)
	now := autoT0
	done := make(chan struct{})
	var logb bytes.Buffer
	go func() {
		runAutoBackups(ctx, st, log.New(&logb, "", 0), func() time.Time { return now }, tick)
		close(done)
	}()
	// The start check happens before the first tick is accepted; an unbuffered
	// send returns only once the loop is back at select, so the start check is done.
	tick <- now
	if n := countAuto(t, dir); n != 1 {
		t.Fatalf("after start: %d automatic backups, want 1", n)
	}
	cancel()
	<-done

	for h := 1; h <= 23; h++ {
		now = autoT0.Add(time.Duration(h) * time.Hour)
		if p, err := st.AutoBackupCheck(now, def); err != nil || p != "" {
			t.Fatalf("hour %d: wrote %q err %v; want nothing within 24h", h, p, err)
		}
	}
	if n := countAuto(t, dir); n != 1 {
		t.Fatalf("within 24h: %d, want 1", n)
	}
	now = autoT0.Add(24 * time.Hour)
	if p, err := st.AutoBackupCheck(now, def); err != nil || p == "" {
		t.Fatalf("at 24h: wrote %q err %v; want one", p, err)
	}
	if n := countAuto(t, dir); n != 2 {
		t.Fatalf("after 24h: %d, want 2", n)
	}
	for h := 25; h <= 9*24; h++ {
		if _, err := st.AutoBackupCheck(autoT0.Add(time.Duration(h)*time.Hour), def); err != nil {
			t.Fatal(err)
		}
	}
	files, _ := store.ListAutoBackups(dir)
	if len(files) != 7 {
		t.Fatalf("after 9 days: %d automatic backups, want 7: %v", len(files), autoFiles(t, dir))
	}
	for i, f := range files {
		want := autoT0.Add(time.Duration(9-i) * 24 * time.Hour)
		if !f.At.Equal(want) {
			t.Errorf("kept[%d] = %s, want %s", i, f.At, want)
		}
	}
	if b, err := os.ReadFile(manual); err != nil || string(b) != "manual" { //nolint:gosec // test-owned temp file
		t.Fatalf("manual backup touched: %q %v", b, err)
	}
}

func TestBackupAutoSettings(t *testing.T) {
	st, dir := openBackupEnvStore(t)
	def, _ := defaultBackupDir()

	if code, out, _ := runAutoCLI(t, "backup", "auto"); code != 0 || !strings.Contains(out, "automatic backups: on") || !strings.Contains(out, "keep: 7") || !strings.Contains(out, "folder: "+dir) {
		t.Fatalf("show: %d %q", code, out)
	}
	if _, err := st.AutoBackupCheck(autoT0, def); err != nil {
		t.Fatal(err)
	}

	if code, _, e := runAutoCLI(t, "backup", "auto", "--off"); code != 0 {
		t.Fatalf("--off: %d %s", code, e)
	}
	for d := 1; d <= 3; d++ {
		if _, err := st.AutoBackupCheck(autoT0.Add(time.Duration(d)*24*time.Hour), def); err != nil {
			t.Fatal(err)
		}
	}
	if n := countAuto(t, dir); n != 1 {
		t.Fatalf("off for 3 days: %d automatic backups, want 1", n)
	}
	if code, out, _ := runAutoCLI(t, "backup", "auto"); code != 0 || !strings.Contains(out, "automatic backups: off") {
		t.Fatalf("show off: %q", out)
	}
	if code, _, e := runAutoCLI(t, "backup", "auto", "--on"); code != 0 {
		t.Fatalf("--on: %d %s", code, e)
	}
	if _, err := st.AutoBackupCheck(autoT0.Add(4*24*time.Hour), def); err != nil {
		t.Fatal(err)
	}
	if n := countAuto(t, dir); n != 2 {
		t.Fatalf("after --on: %d, want 2", n)
	}

	// --dir moves where the next one is written, and plain `backup` follows.
	newDir := t.TempDir()
	if code, _, e := runAutoCLI(t, "backup", "auto", "--dir", newDir); code != 0 {
		t.Fatalf("--dir: %d %s", code, e)
	}
	if p, err := st.AutoBackupCheck(autoT0.Add(5*24*time.Hour), def); err != nil || filepath.Dir(p) != newDir {
		t.Fatalf("next backup %q err %v; want in %s", p, err, newDir)
	}
	if code, out, _ := runAutoCLI(t, "backup", "--json"); code != 0 || !strings.Contains(out, newDir) {
		t.Fatalf("manual backup not in new dir: %q", out)
	}

	// Bad dirs exit non-zero and change nothing.
	// A path under a regular file (ENOTDIR) fails the existence check for
	// root too. /proc/self is an existing folder no one can create a file in,
	// root included, so it reaches the writability probe for everyone. The
	// read-only dir only fails for non-root, which ignores no modes; whether it
	// is included is decided by the test's own probe, never by checkBackupDir.
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	bads := []string{filepath.Join(t.TempDir(), "missing"), filepath.Join(blocker, "sub"), "/proc/self"}
	ro := t.TempDir()
	if err := os.Chmod(ro, 0o500); err != nil { //nolint:gosec // read-only dir is the failure under test
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) }) //nolint:gosec // restore for TempDir cleanup
	if f, err := os.CreateTemp(ro, "probe-*"); err != nil {
		bads = append(bads, ro)
	} else {
		_ = f.Close()
		_ = os.Remove(f.Name())
		t.Log("read-only dir is still writable here (root); /proc/self covers the writability refusal")
	}
	for _, bad := range bads {
		if code, _, e := runAutoCLI(t, "backup", "auto", "--dir", bad); code == 0 || !strings.Contains(e, "nothing changed") {
			t.Fatalf("--dir %s: exit %d stderr %q", bad, code, e)
		}
	}
	cfg, _ := st.AutoBackupConfig(def)
	if cfg.Dir != newDir {
		t.Fatalf("dir changed by a bad --dir: %s", cfg.Dir)
	}

	// --keep trims at the next run.
	for d := 6; d <= 9; d++ {
		if _, err := st.AutoBackupCheck(autoT0.Add(time.Duration(d)*24*time.Hour), def); err != nil {
			t.Fatal(err)
		}
	}
	if code, _, _ := runAutoCLI(t, "backup", "auto", "--keep", "0"); code == 0 {
		t.Fatal("--keep 0 accepted")
	}
	if code, _, e := runAutoCLI(t, "backup", "auto", "--keep", "3"); code != 0 {
		t.Fatalf("--keep: %d %s", code, e)
	}
	if _, err := st.AutoBackupCheck(autoT0.Add(9*24*time.Hour+time.Hour), def); err != nil {
		t.Fatal(err)
	}
	if n := countAuto(t, newDir); n != 3 {
		t.Fatalf("after --keep 3: %d, want 3", n)
	}
}

// A failing backup keeps what exists; past 48h status and this-week say so.
func TestAutoBackupFailureKeepsBackupsAndRaisesAttention(t *testing.T) {
	st, dir := openBackupEnvStore(t)
	def, _ := defaultBackupDir()
	for d := 0; d < 2; d++ {
		if _, err := st.AutoBackupCheck(autoT0.Add(time.Duration(d)*24*time.Hour), def); err != nil {
			t.Fatal(err)
		}
	}
	lastOK := autoT0.Add(24 * time.Hour)
	// Every attempt hour finds a folder where its backup file would go, so the
	// write fails with "exists" for root too (a read-only dir would not).
	// Folders are not backups: they are not listed or counted.
	for h := 24; h <= 47; h++ {
		blocked := filepath.Join(dir, store.AutoBackupName(lastOK.Add(time.Duration(h)*time.Hour)))
		if err := os.Mkdir(blocked, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	oldB, oldW := backupNow, thisWeekNow
	t.Cleanup(func() { backupNow, thisWeekNow = oldB, oldW })
	statusAt := func(at time.Time) string {
		backupNow = func() time.Time { return at }
		var out bytes.Buffer
		printBackupLines(&out)
		return out.String()
	}
	weekAttention := func(at time.Time) []string {
		thisWeekNow = func() time.Time { return at }
		code, out, e := runAutoCLI(t, "this-week", "--json")
		if code != 0 {
			t.Fatalf("this-week: %d %s", code, e)
		}
		var res struct {
			Attention []struct {
				Kind, Reason string
			} `json:"attention"`
		}
		if err := json.Unmarshal([]byte(out), &res); err != nil {
			t.Fatal(err)
		}
		var kinds []string
		for _, a := range res.Attention {
			if a.Kind == "backup-failing" {
				kinds = append(kinds, a.Reason)
			}
		}
		return kinds
	}

	if out := statusAt(lastOK.Add(2 * time.Hour)); !strings.Contains(out, "backups: last automatic 2h ago, 2 kept in "+dir) || strings.Contains(out, "NEEDS YOU") {
		t.Fatalf("healthy status: %q", out)
	}
	for h := 24; h <= 47; h++ { // due from 24h on; every attempt fails
		if _, err := st.AutoBackupCheck(lastOK.Add(time.Duration(h)*time.Hour), def); err == nil {
			t.Fatalf("hour %d: backup over a blocked name succeeded", h)
		}
	}
	if n := countAuto(t, dir); n != 2 {
		t.Fatalf("failed backups lost files: %d, want 2", n)
	}
	if got := weekAttention(lastOK.Add(47 * time.Hour)); len(got) != 0 {
		t.Fatalf("attention at 47h: %v", got)
	}
	at := lastOK.Add(49 * time.Hour)
	out := statusAt(at)
	if !strings.Contains(out, "NEEDS YOU: automatic backups are failing: last ok ") || !strings.Contains(out, "backups: last automatic 2d ago") {
		t.Fatalf("failing status: %q", out)
	}
	if got := weekAttention(at); len(got) != 1 || !strings.HasPrefix(got[0], "automatic backups are failing: last ok ") {
		t.Fatalf("this-week attention: %v", got)
	}

	// Off: the line says so and there is no attention item.
	if code, _, e := runAutoCLI(t, "backup", "auto", "--off"); code != 0 {
		t.Fatal(e)
	}
	if out := statusAt(at); strings.TrimSpace(out) != "backups: automatic off" {
		t.Fatalf("off status: %q", out)
	}
	if got := weekAttention(at); len(got) != 0 {
		t.Fatalf("attention while off: %v", got)
	}
}
