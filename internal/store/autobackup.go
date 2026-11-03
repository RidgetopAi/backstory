package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Settings keys and defaults for automatic backups (human-only settings, like
// every setting; the agent socket exposes no settings write).
const (
	SettingBackupAuto  = "backup_auto"
	SettingBackupDir   = "backup_dir"
	SettingBackupKeep  = "backup_keep"
	settingBackupSince = "backup_auto_since"

	// DefaultBackupKeep is how many automatic backups are kept; MinBackupKeep
	// is the floor --keep accepts.
	DefaultBackupKeep = 7
	MinBackupKeep     = 1

	// AutoBackupInterval is the age of the newest automatic backup at which
	// the next one is written. AutoBackupFailingAfter is the age at which the
	// absence of a newer one becomes a NEEDS YOU item. AutoBackupCheckEvery
	// is how often the daemon checks.
	AutoBackupInterval     = 24 * time.Hour
	AutoBackupFailingAfter = 48 * time.Hour
	AutoBackupCheckEvery   = time.Hour

	// BackupTimeLayout is the UTC stamp in a backup file name.
	BackupTimeLayout = "20060102T150405Z"

	autoBackupPrefix = "backstory-auto-"
	autoBackupSuffix = ".db"
)

// AutoBackupConfig is the effective automatic-backup configuration.
type AutoBackupConfig struct {
	On   bool
	Dir  string
	Keep int
}

// AutoBackupFile is one automatic backup found in a backup directory.
type AutoBackupFile struct {
	Path string
	At   time.Time // the UTC stamp in its name
}

// AutoBackupName is the file name of an automatic backup made at t.
func AutoBackupName(t time.Time) string {
	return autoBackupPrefix + t.UTC().Format(BackupTimeLayout) + autoBackupSuffix
}

// AutoBackupConfig reads the settings; defaultDir is used when backup_dir is
// unset. An unparsable keep falls back to the default.
func (s *Store) AutoBackupConfig(defaultDir string) (AutoBackupConfig, error) {
	cfg := AutoBackupConfig{On: true, Dir: defaultDir, Keep: DefaultBackupKeep}
	if v, ok, err := s.GetSetting(SettingBackupAuto); err != nil {
		return cfg, err
	} else if ok {
		cfg.On = v != "off"
	}
	if v, ok, err := s.GetSetting(SettingBackupDir); err != nil {
		return cfg, err
	} else if ok && v != "" {
		cfg.Dir = v
	}
	if v, ok, err := s.GetSetting(SettingBackupKeep); err != nil {
		return cfg, err
	} else if ok {
		if n, perr := strconv.Atoi(v); perr == nil && n >= MinBackupKeep {
			cfg.Keep = n
		}
	}
	return cfg, nil
}

// ListAutoBackups returns the automatic backups in dir, newest first. Only
// files named exactly backstory-auto-<UTC ts>.db count; manual backups and any
// other file are ignored. A missing dir yields none.
func ListAutoBackups(dir string) ([]AutoBackupFile, error) {
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: list automatic backups: %w", err)
	}
	var out []AutoBackupFile
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, autoBackupPrefix) || !strings.HasSuffix(name, autoBackupSuffix) {
			continue
		}
		stamp := strings.TrimSuffix(strings.TrimPrefix(name, autoBackupPrefix), autoBackupSuffix)
		at, perr := time.Parse(BackupTimeLayout, stamp)
		if perr != nil {
			continue
		}
		out = append(out, AutoBackupFile{Path: filepath.Join(dir, name), At: at})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out, nil
}

// AutoBackupCheck writes an automatic backup when automatic backups are on and
// the newest one in the configured dir is at least AutoBackupInterval old at
// now (or none exists), then deletes the oldest automatic backups beyond the
// keep count. It reports the path written ("" when nothing was due). A failed
// backup leaves every existing backup alone; the caller logs and retries at
// the next check. Trimming also runs when nothing is due, so a lowered
// --keep takes effect at the next check.
func (s *Store) AutoBackupCheck(now time.Time, defaultDir string) (string, error) {
	cfg, err := s.AutoBackupConfig(defaultDir)
	if err != nil {
		return "", err
	}
	if !cfg.On {
		return "", nil
	}
	if err := s.noteAutoBackupSince(now); err != nil {
		return "", err
	}
	files, err := ListAutoBackups(cfg.Dir)
	if err != nil {
		return "", err
	}
	written := ""
	if len(files) == 0 || now.Sub(files[0].At) >= AutoBackupInterval {
		dest := filepath.Join(cfg.Dir, AutoBackupName(now))
		if _, err := s.Backup(dest, false); err != nil {
			return "", err
		}
		written = dest
		if files, err = ListAutoBackups(cfg.Dir); err != nil {
			return written, err
		}
	}
	for i := cfg.Keep; i < len(files); i++ {
		if err := os.Remove(files[i].Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return written, fmt.Errorf("store: trim automatic backups: %w", err)
		}
	}
	return written, nil
}

// noteAutoBackupSince records when automatic backups were first checked, once.
func (s *Store) noteAutoBackupSince(now time.Time) error {
	if _, ok, err := s.GetSetting(settingBackupSince); err != nil || ok {
		return err
	}
	return s.SetSetting(settingBackupSince, now.UTC().Format(time.RFC3339))
}

// AutoBackupHealth is the state status and this-week report.
type AutoBackupHealth struct {
	Config  AutoBackupConfig
	Kept    int
	Last    time.Time // newest automatic backup; zero when none
	Failing bool
}

// AutoBackupHealthAt inspects the configured dir. Failing is true when
// automatic backups are on and the newest is older than AutoBackupFailingAfter
// (or none exists that long after automatic backups were first checked).
func (s *Store) AutoBackupHealthAt(now time.Time, defaultDir string) (AutoBackupHealth, error) {
	cfg, err := s.AutoBackupConfig(defaultDir)
	if err != nil {
		return AutoBackupHealth{}, err
	}
	h := AutoBackupHealth{Config: cfg}
	files, err := ListAutoBackups(cfg.Dir)
	if err != nil {
		return h, err
	}
	h.Kept = len(files)
	ref := time.Time{}
	if len(files) > 0 {
		h.Last = files[0].At
		ref = h.Last
	} else if v, ok, err := s.GetSetting(settingBackupSince); err != nil {
		return h, err
	} else if ok {
		ref, _ = time.Parse(time.RFC3339, v)
	}
	h.Failing = cfg.On && !ref.IsZero() && now.Sub(ref) > AutoBackupFailingAfter
	return h, nil
}
