package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// backupTimeLayout is the UTC stamp in a default backup file name.
const backupTimeLayout = "20060102T150405Z"

// backupDir is <data dir>/backstory/backups, beside the store.
func backupDir() (string, error) {
	db, err := storePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(db), "backups"), nil
}

// runBackup is `backstory backup [--out PATH] [--force] [--json]`: a
// human-path command that opens the store directly (no daemon needed) and
// writes a checked, consistent snapshot of it (Store.Backup).
func runBackup(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	outFlag := fs.String("out", "", "write the snapshot exactly here (default: <data dir>/backstory/backups/backstory-<UTC time>.db)")
	force := fs.Bool("force", false, "overwrite --out if it already exists")
	jsonOut := fs.Bool("json", false, "print the result as a JSON object")
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "backstory backup: unexpected argument %q\n", fs.Arg(0))
		return 2
	}

	dest := *outFlag
	if dest == "" {
		dir, err := backupDir()
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory backup:", err)
			return 1
		}
		dest = filepath.Join(dir, "backstory-"+time.Now().UTC().Format(backupTimeLayout)+".db")
	}

	dbPath, err := storePath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory backup:", err)
		return 1
	}
	if _, err := os.Stat(dbPath); err != nil {
		_, _ = fmt.Fprintf(stderr, "backstory backup: no store at %s: %v\n", dbPath, err)
		return 1
	}
	st, err := openStore(dbPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory backup:", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	info, err := st.Backup(dest, *force)
	if err != nil {
		if errors.Is(err, store.ErrBackupExists) {
			_, _ = fmt.Fprintf(stderr, "backstory backup: %s already exists (use --force to overwrite)\n", dest)
			return 1
		}
		_, _ = fmt.Fprintln(stderr, "backstory backup:", err)
		return 1
	}

	if *jsonOut {
		b, err := json.Marshal(info)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory backup:", err)
			return 1
		}
		_, _ = fmt.Fprintln(stdout, string(b))
		return 0
	}
	_, _ = fmt.Fprintf(stdout, "path: %s\nsize: %d bytes\nschema version: %d\nprojects: %d\nsessions: %d\nrecords: %d\nnewest activity: %s\n",
		info.Path, info.Size, info.SchemaVersion, info.Projects, info.Sessions, info.Records, newestActivityText(info.Newest))
	return 0
}
