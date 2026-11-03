package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
	"github.com/RidgetopAi/backstory/internal/week"
)

// backupNow is the clock status reads; a test pins it.
var backupNow = time.Now

// effectiveBackupDir is the backup_dir setting, else <data dir>/backstory/backups.
func effectiveBackupDir(st *store.Store) (string, error) {
	def, err := defaultBackupDir()
	if err != nil {
		return "", err
	}
	cfg, err := st.AutoBackupConfig(def)
	if err != nil {
		return "", err
	}
	return cfg.Dir, nil
}

// runAutoBackups is the daemon's automatic-backup loop: one check now, then
// one per tick until ctx ends. now and tick are injected so a test drives it
// with a fake clock and no sleeping. A failed backup is logged and retried at
// the next tick.
func runAutoBackups(ctx context.Context, st *store.Store, logger *log.Logger, now func() time.Time, tick <-chan time.Time) {
	def, err := defaultBackupDir()
	if err != nil {
		logger.Printf("automatic backup: %v", err)
		return
	}
	check := func() {
		path, err := st.AutoBackupCheck(now(), def)
		switch {
		case err != nil:
			logger.Printf("automatic backup failed (will retry): %v", err)
		case path != "":
			logger.Printf("automatic backup written: %s", path)
		}
	}
	check()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
			check()
		}
	}
}

// checkBackupDir is nil when dir exists, is a directory and can be written to.
func checkBackupDir(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a folder", dir)
	}
	f, err := os.CreateTemp(dir, ".backstory-writable-*.tmp")
	if err != nil {
		return fmt.Errorf("%s is not writable: %w", dir, err)
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return nil
}

// runBackupAuto is `backstory backup auto [--on|--off] [--dir PATH] [--keep N]`:
// with no flag it shows the setting; every flag is validated before any is stored.
func runBackupAuto(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("backup auto", flag.ContinueOnError)
	on := fs.Bool("on", false, "turn automatic backups on")
	off := fs.Bool("off", false, "turn automatic backups off")
	dir := fs.String("dir", "", "write automatic backups (and `backup` without --out) into this existing, writable folder")
	keep := fs.Int("keep", 0, "keep this many automatic backups (at least 1; default 7)")
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "backstory backup auto: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if *on && *off {
		_, _ = fmt.Fprintln(stderr, "backstory backup auto: --on and --off together")
		return 2
	}
	if set["keep"] && *keep < store.MinBackupKeep {
		_, _ = fmt.Fprintf(stderr, "backstory backup auto: --keep must be at least %d\n", store.MinBackupKeep)
		return 2
	}
	absDir := ""
	if set["dir"] {
		abs, err := filepath.Abs(*dir)
		if err == nil {
			err = checkBackupDir(abs)
		}
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "backstory backup auto: --dir: %v (nothing changed)\n", err)
			return 1
		}
		absDir = abs
	}

	dbPath, err := storePath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory backup auto:", err)
		return 1
	}
	if _, err := os.Stat(dbPath); err != nil {
		_, _ = fmt.Fprintf(stderr, "backstory backup auto: no store at %s: %v\n", dbPath, err)
		return 1
	}
	st, err := openStore(dbPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory backup auto:", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	if *on || *off {
		val := "off"
		if *on {
			val = "on"
		}
		if err := st.SetSetting(store.SettingBackupAuto, val); err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory backup auto:", err)
			return 1
		}
	}
	if set["dir"] {
		if err := st.SetSetting(store.SettingBackupDir, absDir); err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory backup auto:", err)
			return 1
		}
	}
	if set["keep"] {
		if err := st.SetSetting(store.SettingBackupKeep, strconv.Itoa(*keep)); err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory backup auto:", err)
			return 1
		}
	}

	def, err := defaultBackupDir()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory backup auto:", err)
		return 1
	}
	h, err := st.AutoBackupHealthAt(time.Now(), def)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory backup auto:", err)
		return 1
	}
	state := "on"
	if !h.Config.On {
		state = "off"
	}
	last := "never"
	if !h.Last.IsZero() {
		last = h.Last.Local().Format("2006-01-02 15:04") + " (" + agoText(time.Since(h.Last)) + ")"
	}
	_, _ = fmt.Fprintf(stdout, "automatic backups: %s\nfolder: %s\nkeep: %d\nlast automatic: %s\nkept: %d\n",
		state, h.Config.Dir, h.Config.Keep, last, h.Kept)
	return 0
}

// agoText is a short "3h ago" style age.
func agoText(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
}

// backupHealth reads the automatic-backup state; ok is false when it cannot be read.
func backupHealth(st *store.Store, now time.Time) (store.AutoBackupHealth, bool) {
	def, err := defaultBackupDir()
	if err != nil {
		return store.AutoBackupHealth{}, false
	}
	h, err := st.AutoBackupHealthAt(now, def)
	return h, err == nil
}

// backupFailingReason is the NEEDS YOU line for failing automatic backups.
func backupFailingReason(h store.AutoBackupHealth) string {
	last := "never"
	if !h.Last.IsZero() {
		last = h.Last.Local().Format("2006-01-02 15:04")
	}
	return "automatic backups are failing: last ok " + last
}

// backupFailingAttention is the this-week / panel item: kind backup-failing,
// project_key "backup".
func backupFailingAttention(st *store.Store, now time.Time) []week.AttentionItem {
	h, ok := backupHealth(st, now)
	if !ok || !h.Failing {
		return nil
	}
	return []week.AttentionItem{{Kind: week.AttentionBackupFailing, ProjectKey: "backup", Reason: backupFailingReason(h), EvidenceIDs: []string{}}}
}

// printBackupLines prints status's `backups:` line and, when failing, the
// NEEDS YOU line. A store that cannot be read is skipped quietly.
func printBackupLines(stdout io.Writer) {
	dbPath, err := storePath()
	if err != nil {
		return
	}
	if _, err := os.Stat(dbPath); err != nil {
		return
	}
	st, err := openStore(dbPath)
	if err != nil {
		return
	}
	defer func() { _ = st.Close() }()
	now := backupNow()
	h, ok := backupHealth(st, now)
	if !ok {
		return
	}
	switch {
	case !h.Config.On:
		_, _ = fmt.Fprintln(stdout, "backups: automatic off")
	case h.Last.IsZero():
		_, _ = fmt.Fprintf(stdout, "backups: none yet, automatic on, folder %s\n", h.Config.Dir)
	default:
		_, _ = fmt.Fprintf(stdout, "backups: last automatic %s, %d kept in %s\n", agoText(now.Sub(h.Last)), h.Kept, h.Config.Dir)
	}
	if h.Failing {
		_, _ = fmt.Fprintln(stdout, "NEEDS YOU: "+backupFailingReason(h))
	}
}
