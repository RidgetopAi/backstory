package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// restoreUnits are the systemd user units restore stops while it swaps the
// store, in stop order (ops/install.sh names them); they restart in reverse.
var restoreUnits = []string{"backstory-sync.timer", "backstory-sync.service", "backstory.service"}

// preRestoreInfix names the copy of the replaced store, beside the store:
// backstory.db.pre-restore-<UTC ts>. Restore never deletes it.
const preRestoreInfix = ".pre-restore-"

// restoreFailHook, when set (tests only), is called at the named step of the
// swap — "rename" before the new file is moved into place, "open" before the
// post-swap Open — and a non-nil return fails that step.
var restoreFailHook func(step string) error

func restoreHook(step string) error {
	if restoreFailHook != nil {
		return restoreFailHook(step)
	}
	return nil
}

// runRestore is `backstory restore PATH [--yes]`: verify the backup, show what
// it holds beside what the store holds, stop the daemon and sync units, keep a
// consistent copy of the current store aside, swap the backup in, open it once
// (running migrations) and restart what was running. Any failure after the
// stop puts the original store back.
func runRestore(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	fs.SetOutput(stderr)
	// flag stops at the first operand; accept --yes on either side of PATH.
	var operands []string
	for rest := args; ; {
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		if fs.NArg() == 0 {
			break
		}
		operands = append(operands, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if len(operands) != 1 {
		_, _ = fmt.Fprintln(stderr, "usage: backstory restore PATH [--yes]")
		return 2
	}
	fail := func(format string, a ...any) int {
		_, _ = fmt.Fprintf(stderr, "backstory restore: "+format+"\n", a...)
		return 1
	}

	src, err := filepath.Abs(operands[0])
	if err != nil {
		return fail("%v", err)
	}
	dbPath, err := storePath()
	if err != nil {
		return fail("%v", err)
	}

	// VERIFY FIRST: nothing changes until the backup passes.
	if fi, err := os.Stat(src); err != nil || !fi.Mode().IsRegular() {
		return fail("%s is not a readable backup file", src)
	}
	if same, _ := sameFile(src, dbPath); same {
		return fail("%s is the live store, not a backup", src)
	}
	backup, err := store.VerifyBackup(src)
	if err != nil {
		return fail("%s is not a usable Backstory backup: %v", src, err)
	}
	if backup.SchemaVersion > store.SchemaVersion {
		return fail("this backup is from a newer Backstory (schema %d, this one knows %d); upgrade first", backup.SchemaVersion, store.SchemaVersion)
	}
	var current *store.BackupInfo
	if _, err := os.Stat(dbPath); err == nil {
		c, err := store.VerifyBackup(dbPath)
		if err != nil {
			return fail("the current store at %s cannot be checked, so it cannot be kept aside safely: %v", dbPath, err)
		}
		current = &c
	}

	_, _ = fmt.Fprintf(stdout, "backup:  %s\n", describeStore(backup))
	if current != nil {
		_, _ = fmt.Fprintf(stdout, "current: %s\n", describeStore(*current))
	} else {
		_, _ = fmt.Fprintln(stdout, "current: no store")
	}
	if !*yes {
		if !isTerminal(stdin) {
			return fail("refusing to restore without --yes on a non-interactive session")
		}
		_, _ = fmt.Fprint(stdout, "Replace the current store with this backup? [y/N] ")
		line, _ := bufio.NewReader(stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			_, _ = fmt.Fprintln(stdout, "aborted")
			return 1
		}
	}

	// Find what is running; with no systemd user session, a daemon on the
	// socket is something restore cannot stop, so it refuses.
	sd := detectSystemd()
	var running []string
	if sd {
		for _, u := range restoreUnits {
			if unitActive(u) {
				running = append(running, u)
			}
		}
	} else if daemonOnSocket() {
		return fail("a Backstory daemon is running and there is no systemd user session to stop it; stop it and run restore again")
	}

	restart := func() error {
		var errs []error
		for i := len(running) - 1; i >= 0; i-- {
			if out, err := exec.Command("systemctl", "--user", "start", running[i]).CombinedOutput(); err != nil { //nolint:gosec // fixed unit names
				errs = append(errs, fmt.Errorf("start %s: %v: %s", running[i], err, strings.TrimSpace(string(out))))
			}
		}
		return errors.Join(errs...)
	}
	for _, u := range running {
		if out, err := exec.Command("systemctl", "--user", "stop", u).CombinedOutput(); err != nil { //nolint:gosec // fixed unit names
			_ = restart()
			return fail("stopping %s failed, nothing was changed: %v: %s", u, err, strings.TrimSpace(string(out)))
		}
	}
	if !sd || len(running) > 0 {
		if daemonOnSocket() {
			_ = restart()
			return fail("a Backstory daemon still holds the store after stopping the units, nothing was changed; stop it and run restore again")
		}
	}

	// Everything below is after the stop: any failure rolls back.
	var kept string
	rollback := func(cause string) int {
		code := 1
		_, _ = fmt.Fprintf(stderr, "backstory restore: FAILED: %s\n", cause)
		if kept != "" {
			if err := placeFile(kept, dbPath, false); err != nil {
				_, _ = fmt.Fprintf(stderr, "backstory restore: could not put the previous store back: %v\n  it is safe at %s\n", err, kept)
			} else {
				_, _ = fmt.Fprintln(stderr, "backstory restore: the previous store is back in place")
			}
		}
		if err := restart(); err != nil {
			_, _ = fmt.Fprintf(stderr, "backstory restore: restart failed: %v\n", err)
		}
		return code
	}

	if current != nil {
		kept = dbPath + preRestoreInfix + time.Now().UTC().Format(backupTimeLayout)
		cur, err := openStore(dbPath)
		if err != nil {
			kept = ""
			return rollback(fmt.Sprintf("opening the current store: %v", err))
		}
		_, err = cur.Backup(kept, false)
		_ = cur.Close()
		if err != nil {
			kept = ""
			return rollback(fmt.Sprintf("keeping the current store aside: %v", err))
		}
	}
	if err := placeFile(src, dbPath, true); err != nil {
		return rollback(fmt.Sprintf("swapping the backup in: %v", err))
	}
	restored, err := openOnce(dbPath)
	if err != nil {
		return rollback(fmt.Sprintf("opening the restored store: %v", err))
	}
	if err := restart(); err != nil {
		return rollback(fmt.Sprintf("restarting services: %v", err))
	}

	_, _ = fmt.Fprintf(stdout, "restored: %s\n", describeStore(restored))
	if kept != "" {
		_, _ = fmt.Fprintf(stdout, "previous store kept at: %s\n", kept)
	}
	if len(running) > 0 {
		_, _ = fmt.Fprintf(stdout, "restarted: %s (the daemon is running again)\n", strings.Join(reverse(running), ", "))
	} else {
		_, _ = fmt.Fprintln(stdout, "no Backstory service was running, so none was restarted")
	}
	return 0
}

func reverse(s []string) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[len(s)-1-i] = v
	}
	return out
}

func describeStore(i store.BackupInfo) string {
	newest := i.Newest
	if newest == "" {
		newest = "none"
	}
	return fmt.Sprintf("%d projects, %d sessions, %d records, newest activity %s", i.Projects, i.Sessions, i.Records, newest)
}

func sameFile(a, b string) (bool, error) {
	ai, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	return os.SameFile(ai, bi), nil
}

// detectSystemd reports whether a systemd user session answers systemctl.
func detectSystemd() bool {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return false
	}
	return exec.Command("systemctl", "--user", "show-environment").Run() == nil
}

func unitActive(unit string) bool {
	return exec.Command("systemctl", "--user", "is-active", "--quiet", unit).Run() == nil //nolint:gosec // fixed unit names
}

// daemonOnSocket reports whether something answers on the daemon socket.
func daemonOnSocket() bool {
	p, err := socketPath()
	if err != nil {
		return false
	}
	c, err := net.DialTimeout("unix", p, time.Second)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// placeFile copies src to a temp file beside dst, fsyncs it, drops dst's stale
// -wal/-shm and renames it over dst, then fsyncs the directory. hooked marks
// the forward swap, the only one tests inject failures into.
func placeFile(src, dst string, hooked bool) error {
	in, err := os.Open(src) //nolint:gosec // path the human named
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, store.BackupDirMode); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".backstory-restore-*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // no-op once renamed
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(store.FileMode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if hooked {
		if err := restoreHook("rename"); err != nil {
			return err
		}
	}
	for _, sfx := range []string{"-wal", "-shm"} {
		if err := os.Remove(dst + sfx); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return err
	}
	if d, err := os.Open(dir); err == nil { //nolint:gosec // store dir
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// openOnce opens the swapped-in store through the normal Open (running any
// pending migrations), closes it, and reports what it holds.
func openOnce(dbPath string) (store.BackupInfo, error) {
	if err := restoreHook("open"); err != nil {
		return store.BackupInfo{}, err
	}
	st, err := openStore(dbPath)
	if err != nil {
		return store.BackupInfo{}, err
	}
	if err := st.Close(); err != nil {
		return store.BackupInfo{}, err
	}
	return store.VerifyBackup(dbPath)
}
