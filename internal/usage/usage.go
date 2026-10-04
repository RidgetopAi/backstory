// Package usage writes Backstory's Omarchy usage record — the JSON file
// Omarchy's Agents panel lists from
// ${XDG_STATE_HOME:-$HOME/.local/state}/omarchy/agents/usage (PLAN.md Phase
// 3, AGENT-CONTRACT.md "the Agents panel picks up any record").
package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

const (
	// SchemaVersion is the record's schemaVersion; Omarchy reads 1.
	SchemaVersion = 1
	// AgentID and AgentName identify Backstory in the panel.
	AgentID   = "backstory"
	AgentName = "Backstory"
	// FileName is the record's file name inside the usage folder.
	FileName = AgentID + ".json"
	// FileMode is the record's permission (Omarchy's existing files are 0600).
	FileMode = 0o600
	// DefaultInterval is how often the daemon rewrites the record.
	DefaultInterval = 5 * time.Minute
	// DefaultRecentDays is the width of recentDays, today included.
	DefaultRecentDays = 30
)

// DayCount is one recentDays entry.
type DayCount struct {
	Date         string `json:"date"`
	MessageCount int    `json:"messageCount"`
}

// Record is the Omarchy usage record. Backstory has no prompt concept, so
// the prompt fields are 0 rather than a guess.
type Record struct {
	SchemaVersion   int        `json:"schemaVersion"`
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	UpdatedAt       string     `json:"updatedAt"`
	Ready           bool       `json:"ready"`
	TodayPrompts    int        `json:"todayPrompts"`
	TodaySessions   int        `json:"todaySessions"`
	TotalPrompts    int        `json:"totalPrompts"`
	TotalSessions   int        `json:"totalSessions"`
	ActiveDays      int        `json:"activeDays"`
	RecentDays      []DayCount `json:"recentDays"`
	ActiveDates     []string   `json:"activeDates"`
	UsageStatusText string     `json:"usageStatusText"`
	HasLocalStats   bool       `json:"hasLocalStats"`
}

// Dir returns the usage folder: $XDG_STATE_HOME/omarchy/agents/usage, or
// $HOME/.local/state/omarchy/agents/usage when XDG_STATE_HOME is empty.
func Dir() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("usage: resolve home dir: %w", err)
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "omarchy", "agents", "usage"), nil
}

// Path returns the full path of backstory.json.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// Options configures Build.
type Options struct {
	Now        time.Time
	Loc        *time.Location
	RecentDays int
	// CaptureOff is whether the capture-off flag is set.
	CaptureOff bool
}

// Build fills a Record from the store.
func Build(st *store.Store, o Options) (Record, error) {
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	if o.RecentDays <= 0 {
		o.RecentDays = DefaultRecentDays
	}
	stats, err := st.UsageStats(o.Now, o.Loc, o.RecentDays)
	if err != nil {
		return Record{}, err
	}
	r := Record{
		SchemaVersion: SchemaVersion,
		ID:            AgentID,
		Name:          AgentName,
		UpdatedAt:     o.Now.UTC().Format(time.RFC3339Nano),
		Ready:         true,
		TodaySessions: stats.TodaySessions,
		TotalSessions: stats.TotalSessions,
		ActiveDays:    len(stats.ActiveDates),
		ActiveDates:   stats.ActiveDates,
		RecentDays:    []DayCount{},
		HasLocalStats: true,
	}
	if r.ActiveDates == nil {
		r.ActiveDates = []string{}
	}
	for _, d := range stats.RecentDays {
		r.RecentDays = append(r.RecentDays, DayCount{Date: d.Date, MessageCount: d.MessageCount})
	}
	summary := fmt.Sprintf("%d sessions today, %d total across %d active days", r.TodaySessions, r.TotalSessions, r.ActiveDays)
	if o.CaptureOff {
		r.UsageStatusText = "Capture is off — " + summary
	} else {
		r.UsageStatusText = "Capture is on — " + summary
	}
	return r, nil
}

// writeTempData writes data to the temp file; a var so a test can inject a
// write failure.
var writeTempData = func(f *os.File, data []byte) error {
	_, err := f.Write(data)
	return err
}

// Write encodes r and writes it atomically (temp file + rename) with mode
// 0600 to path, creating the parent folder. A failure leaves any previous
// file untouched and removes the temp file.
func Write(path string, r Record) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("usage: encode: %w", err)
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("usage: create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".backstory-usage-*")
	if err != nil {
		return fmt.Errorf("usage: create temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := writeTempData(tmp, data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("usage: write %s: %w", tmpPath, err)
	}
	if err := tmp.Chmod(FileMode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("usage: chmod %s: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("usage: sync %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("usage: close %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("usage: rename %s to %s: %w", tmpPath, path, err)
	}
	renamed = true
	return nil
}

// Config configures Run.
type Config struct {
	Store      *store.Store
	Interval   time.Duration
	RecentDays int
	// CaptureOff reports the capture-off flag; nil means capture is on.
	CaptureOff func() (bool, error)
	Logger     *log.Logger
}

// WriteOnce builds the record and writes it to the default path.
func WriteOnce(c Config) error {
	off := false
	if c.CaptureOff != nil {
		var err error
		if off, err = c.CaptureOff(); err != nil {
			return fmt.Errorf("usage: capture state: %w", err)
		}
	}
	path, err := Path()
	if err != nil {
		return err
	}
	rec, err := Build(c.Store, Options{Now: time.Now(), Loc: time.Local, RecentDays: c.RecentDays, CaptureOff: off})
	if err != nil {
		return err
	}
	return Write(path, rec)
}

// Run writes the record immediately and then every Interval until ctx is
// cancelled. Failures are logged and never stop the loop.
func Run(ctx context.Context, c Config) {
	interval := c.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	tick := func() {
		if err := WriteOnce(c); err != nil && c.Logger != nil {
			c.Logger.Printf("usage record: %v", err)
		}
	}
	tick()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			tick()
		}
	}
}
