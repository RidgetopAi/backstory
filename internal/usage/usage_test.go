package usage

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// seed builds a store with 2 sessions today, 1 two days ago, and records:
// 3 today, 1 two days ago, 2 on a day 40 days ago (outside recentDays).
func seed(t *testing.T) (*store.Store, time.Time) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "backstory.db"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Now()
	day := func(back int) time.Time {
		y, m, d := now.Date()
		return time.Date(y, m, d-back, 12, 0, 0, 0, time.Local)
	}
	for i, ts := range []time.Time{day(0), day(0), day(2)} {
		if _, err := st.StartSession(store.StartSessionParams{
			Agent: "claude", CWD: "/p", StartedAt: ts.Add(time.Duration(i) * time.Second), Origin: store.OriginLive,
		}); err != nil {
			t.Fatal(err)
		}
	}
	rec := func(ts time.Time, n int) {
		for i := 0; i < n; i++ {
			if _, err := st.DB().Exec(`INSERT INTO records (id, ts, kind, tier, text) VALUES (?, ?, 'note', 'agent-declared', 'x')`,
				ts.Format(time.RFC3339Nano)+string(rune('a'+i)), ts.Add(time.Duration(i)*time.Second).UnixNano()); err != nil {
				t.Fatal(err)
			}
		}
	}
	rec(day(0), 3)
	rec(day(2), 1)
	rec(day(40), 2)
	return st, now
}

func readRecord(t *testing.T, path string) Record {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func dayStr(now time.Time, back int) string {
	y, m, d := now.Date()
	return time.Date(y, m, d-back, 12, 0, 0, 0, time.Local).Format("2006-01-02")
}

func TestWriteOnceFixtureXDGStateHome(t *testing.T) {
	st, now := seed(t)
	state := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", state)
	if err := WriteOnce(Config{Store: st}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(state, "omarchy", "agents", "usage", "backstory.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"todaySessions", "totalSessions", "activeDays"} {
		if _, ok := raw[k].(float64); !ok {
			t.Errorf("%s is not a number: %v", k, raw[k])
		}
	}
	if _, ok := raw["ready"].(bool); !ok {
		t.Errorf("ready is not a bool: %v", raw["ready"])
	}
	r := readRecord(t, path)
	if r.SchemaVersion != 1 || r.ID != "backstory" || r.Name == "" || r.UsageStatusText == "" {
		t.Errorf("identity fields wrong: %+v", r)
	}
	if _, err := time.Parse(time.RFC3339, r.UpdatedAt); err != nil {
		t.Errorf("updatedAt: %v", err)
	}
	if !r.Ready || !r.HasLocalStats {
		t.Errorf("ready/hasLocalStats: %+v", r)
	}
	if r.TodaySessions != 2 || r.TotalSessions != 3 {
		t.Errorf("sessions today/total = %d/%d, want 2/3", r.TodaySessions, r.TotalSessions)
	}
	if r.ActiveDays != 3 {
		t.Errorf("activeDays = %d, want 3 (today, -2, -40)", r.ActiveDays)
	}
	want := []DayCount{{dayStr(now, 2), 1}, {dayStr(now, 0), 3}}
	if len(r.RecentDays) != 2 || r.RecentDays[0] != want[0] || r.RecentDays[1] != want[1] {
		t.Errorf("recentDays = %+v, want %+v", r.RecentDays, want)
	}
	if len(r.ActiveDates) != 3 {
		t.Errorf("activeDates = %v", r.ActiveDates)
	}
}

func TestWriteOnceEmptyXDGStateHomeFallsBackToHome(t *testing.T) {
	st, _ := seed(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	if err := WriteOnce(Config{Store: st}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "state", "omarchy", "agents", "usage", "backstory.json")); err != nil {
		t.Fatal(err)
	}
}

func TestWriteModeAndFailureLeavesPreviousIntact(t *testing.T) {
	st, _ := seed(t)
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	if err := WriteOnce(Config{Store: st}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(state, "omarchy", "agents", "usage")
	path := filepath.Join(dir, "backstory.json")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
	before, _ := os.ReadFile(path)

	orig := writeTempData
	writeTempData = func(f *os.File, data []byte) error {
		_, _ = f.Write(data[:len(data)/2])
		return errors.New("injected write failure")
	}
	defer func() { writeTempData = orig }()
	if err := WriteOnce(Config{Store: st}); err == nil {
		t.Fatal("want write error")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("previous content changed after failed write")
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 || ents[0].Name() != "backstory.json" {
		t.Errorf("folder not clean: %v", ents)
	}
}

func TestCaptureOffStatusText(t *testing.T) {
	st, _ := seed(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := WriteOnce(Config{Store: st, CaptureOff: func() (bool, error) { return true, nil }}); err != nil {
		t.Fatal(err)
	}
	p, _ := Path()
	r := readRecord(t, p)
	if !strings.Contains(strings.ToLower(r.UsageStatusText), "capture is off") {
		t.Errorf("status text = %q", r.UsageStatusText)
	}
	if r.TotalSessions != 3 || r.TodaySessions != 2 || r.ActiveDays != 3 {
		t.Errorf("counts missing with capture off: %+v", r)
	}
}

func TestRunWritesAtStartAndOnIntervalAndStopsOnCancel(t *testing.T) {
	st, _ := seed(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	p, _ := Path()
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		Run(ctx, Config{Store: st, Interval: 50 * time.Millisecond, Logger: log.New(os.Stderr, "", 0),
			CaptureOff: func() (bool, error) { calls.Add(1); return false, nil }})
	}()
	waitFor := func(cond func() bool) {
		t.Helper()
		for i := 0; i < 200; i++ {
			if cond() {
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatal("timed out")
	}
	waitFor(func() bool { _, err := os.Stat(p); return err == nil })
	first := readRecord(t, p).UpdatedAt
	waitFor(func() bool { return readRecord(t, p).UpdatedAt != first })
	cancel()
	<-done
	n := calls.Load()
	time.Sleep(200 * time.Millisecond)
	if calls.Load() != n {
		t.Error("wrote after context cancelled")
	}
	a, _ := time.Parse(time.RFC3339, first)
	b, _ := time.Parse(time.RFC3339, readRecord(t, p).UpdatedAt)
	if !b.After(a) {
		t.Errorf("updatedAt did not advance: %v -> %v", a, b)
	}
}
