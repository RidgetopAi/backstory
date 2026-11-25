//go:build linux

package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"unsafe"
)

// The tests in this file run the built binary as a subprocess (the shipped
// entry point, the same as the panel) in a NEW SESSION (Setsid), so the
// child never inherits the controlling terminal of whoever runs `go test`:
// "no TTY" is then a fact of the test, not of the shell it was started in.
// The pty tests give the child a pseudo-terminal as its controlling
// terminal (Setctty) and answer the prompt on its master side.

// openPTY opens a pseudo-terminal pair with nothing but the standard
// library (no creack/pty dependency): /dev/ptmx, unlock, then open the
// slave by the number TIOCGPTN reports.
func openPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open /dev/ptmx: %v", err)
	}
	var unlock int32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); errno != 0 { //nolint:gosec // ioctl takes a pointer to a local int; the standard library has no wrapper
		t.Fatalf("TIOCSPTLCK: %v", errno)
	}
	var n uint32
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, master.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); errno != 0 { //nolint:gosec // ioctl takes a pointer to a local int; the standard library has no wrapper
		t.Fatalf("TIOCGPTN: %v", errno)
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open pty slave: %v", err)
	}
	t.Cleanup(func() { _ = master.Close(); _ = slave.Close() })
	return master, slave
}

type eraseRun struct {
	stdout, stderr, tty string
	code                int
}

// runErase runs `backstory args...` with env and stdin. ttyAnswer == ""
// gives the child no controlling terminal at all; otherwise the child's
// controlling terminal is a fresh pty (passed as fd 3, never as stdin) and
// ttyAnswer is typed into it.
func runErase(t *testing.T, bin string, env []string, stdin io.Reader, ttyAnswer string, args ...string) eraseRun {
	t.Helper()
	cmd := exec.Command(bin, args...) //nolint:gosec // bin is the binary this test just built
	cmd.Env = env
	cmd.Stdin = stdin
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	var ttyOut bytes.Buffer
	var wg sync.WaitGroup
	var master *os.File
	if ttyAnswer != "" {
		var slave *os.File
		master, slave = openPTY(t)
		cmd.ExtraFiles = []*os.File{slave}
		cmd.SysProcAttr.Setctty = true
		cmd.SysProcAttr.Ctty = 3
		if _, err := master.WriteString(ttyAnswer); err != nil {
			t.Fatalf("type into pty: %v", err)
		}
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = io.Copy(&ttyOut, master) }()
	}
	err := cmd.Run()
	if master != nil {
		_ = cmd.ExtraFiles[0].Close()
		_ = master.Close()
		wg.Wait()
	}
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run %v: %v", args, err)
	}
	return eraseRun{stdout: out.String(), stderr: errb.String(), tty: ttyOut.String(), code: code}
}

const noTTYRefusal = "must be confirmed by you in a terminal; run it yourself outside the agent"

func TestEraseInsideAgentSession(t *testing.T) {
	bin := buildBackstory(t)

	deleteFixture := func(t *testing.T) (env []string, dbPath, id string) {
		dataDir := t.TempDir()
		dbPath = filepath.Join(dataDir, "backstory", "backstory.db")
		id = seedDeletableRecord(t, dbPath, "proj-agent-delete", "keep me\nunless the human says so")
		return testXDGEnv("XDG_DATA_HOME="+dataDir, "CLAUDECODE=1"), dbPath, id
	}
	newPurgeFixture := func(t *testing.T) (env []string, f purgeFixture) {
		dataDir := t.TempDir()
		f = seedPurgeStore(t, filepath.Join(dataDir, "backstory", "backstory.db"))
		return testXDGEnv("XDG_DATA_HOME="+dataDir, "CLAUDECODE=1"), f
	}
	wantTombstoned := func(t *testing.T, dbPath, id string, want bool) {
		t.Helper()
		if got := mustGetRecord(t, dbPath, id).TombstonedAt != nil; got != want {
			t.Fatalf("record %s tombstoned = %v, want %v", id, got, want)
		}
	}
	wantSessionEvents := func(t *testing.T, f purgeFixture, want int) {
		t.Helper()
		if n := countRows(t, f.dbPath, `SELECT COUNT(*) FROM timeline_events WHERE session_id = ?`, f.a); n != want {
			t.Fatalf("session a has %d events, want %d", n, want)
		}
	}

	t.Run("delete --yes with no tty is refused and deletes nothing", func(t *testing.T) {
		env, dbPath, id := deleteFixture(t)
		r := runErase(t, bin, env, bytes.NewReader(nil), "", "delete", id, "--yes")
		if r.code == 0 {
			t.Fatalf("exit 0, want refusal (stdout %q)", r.stdout)
		}
		for _, want := range []string{"--yes is refused inside an agent session (CLAUDECODE is set", noTTYRefusal} {
			if !strings.Contains(r.stderr, want) {
				t.Errorf("stderr %q lacks %q", r.stderr, want)
			}
		}
		wantTombstoned(t, dbPath, id, false)
	})

	t.Run("purge --yes with no tty is refused and purges nothing", func(t *testing.T) {
		env, f := newPurgeFixture(t)
		before := tableCounts(t, f.dbPath)
		r := runErase(t, bin, env, bytes.NewReader(nil), "", "purge", "--session", f.a, "--yes")
		if r.code == 0 || !strings.Contains(r.stderr, "--yes is refused") || !strings.Contains(r.stderr, noTTYRefusal) {
			t.Fatalf("exit %d stderr %q, want the --yes and no-terminal refusals", r.code, r.stderr)
		}
		if after := tableCounts(t, f.dbPath); after != before {
			t.Fatalf("events/sessions/purge_log = %v after a refused purge, want %v", after, before)
		}
	})

	t.Run("piped yes on stdin with no tty is refused", func(t *testing.T) {
		env, f := newPurgeFixture(t)
		r := runErase(t, bin, env, strings.NewReader(strings.Repeat("y\n", 50)), "", "purge", "--session", f.a)
		if r.code == 0 || !strings.Contains(r.stderr, noTTYRefusal) {
			t.Fatalf("exit %d stderr %q, want the no-terminal refusal", r.code, r.stderr)
		}
		wantSessionEvents(t, f, f.aEvents)

		env2, dbPath, id := deleteFixture(t)
		r = runErase(t, bin, env2, strings.NewReader("y\n"), "", "delete", id)
		if r.code == 0 || !strings.Contains(r.stderr, noTTYRefusal) {
			t.Fatalf("delete: exit %d stderr %q, want the no-terminal refusal", r.code, r.stderr)
		}
		wantTombstoned(t, dbPath, id, false)
	})

	t.Run("y on the controlling terminal proceeds; stdin is not read", func(t *testing.T) {
		env, dbPath, id := deleteFixture(t)
		r := runErase(t, bin, env, strings.NewReader("n\n"), "y\n", "delete", id, "--yes")
		if r.code != 0 {
			t.Fatalf("exit %d stderr %q tty %q, want the tty's y to delete", r.code, r.stderr, r.tty)
		}
		if !strings.Contains(r.tty, "delete? [y/N]") || !strings.Contains(r.tty, "keep me") {
			t.Errorf("tty %q lacks the prompt naming the record", r.tty)
		}
		wantTombstoned(t, dbPath, id, true)

		penv, f := newPurgeFixture(t)
		r = runErase(t, bin, penv, strings.NewReader("n\n"), "y\n", "purge", "--session", f.a)
		if r.code != 0 || !strings.Contains(r.tty, "This cannot be undone. [y/N]") {
			t.Fatalf("purge: exit %d stderr %q tty %q, want the tty's y to purge", r.code, r.stderr, r.tty)
		}
		wantSessionEvents(t, f, 0)
	})

	t.Run("n on the controlling terminal aborts even with y on stdin", func(t *testing.T) {
		env, dbPath, id := deleteFixture(t)
		r := runErase(t, bin, env, strings.NewReader("y\n"), "n\n", "delete", id)
		if r.code == 0 || !strings.Contains(r.stdout, "aborted") {
			t.Fatalf("exit %d stdout %q, want aborted", r.code, r.stdout)
		}
		wantTombstoned(t, dbPath, id, false)

		penv, f := newPurgeFixture(t)
		r = runErase(t, bin, penv, strings.NewReader("y\n"), "n\n", "purge", "--session", f.a, "--yes")
		if r.code == 0 || !strings.Contains(r.stdout, "aborted") {
			t.Fatalf("purge: exit %d stdout %q, want aborted", r.code, r.stdout)
		}
		wantSessionEvents(t, f, f.aEvents)
	})

	t.Run("dry-run stays allowed", func(t *testing.T) {
		env, f := newPurgeFixture(t)
		r := runErase(t, bin, env, bytes.NewReader(nil), "", "purge", "--session", f.a, "--dry-run")
		if r.code != 0 || !strings.HasPrefix(r.stdout, "would purge 1 sessions, 3 events, 0 records") {
			t.Fatalf("exit %d stdout %q stderr %q, want the dry-run preview", r.code, r.stdout, r.stderr)
		}
		wantSessionEvents(t, f, f.aEvents)
	})

	t.Run("outside an agent session --yes with no tty works (the panel path)", func(t *testing.T) {
		env, dbPath, id := deleteFixture(t)
		env = envWithout(env, "CLAUDECODE")
		r := runErase(t, bin, env, bytes.NewReader(nil), "", "delete", id, "--yes")
		if r.code != 0 {
			t.Fatalf("exit %d stderr %q, want the panel's delete --yes to work", r.code, r.stderr)
		}
		wantTombstoned(t, dbPath, id, true)

		penv, f := newPurgeFixture(t)
		penv = envWithout(penv, "CLAUDECODE")
		r = runErase(t, bin, penv, bytes.NewReader(nil), "", "purge", "--session", f.a, "--yes")
		if r.code != 0 {
			t.Fatalf("purge: exit %d stderr %q, want the panel's purge --yes to work", r.code, r.stderr)
		}
		wantSessionEvents(t, f, 0)
	})
}
