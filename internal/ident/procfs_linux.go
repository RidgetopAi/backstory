//go:build linux

package ident

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// RealProcFS reads /proc directly. It is the daemon's production ProcFS;
// tests use a fake tree instead (AGENT-CONTRACT.md §Observed identity — no
// real /proc in tests, except one integration-tagged self-test of this type
// against the current process).
type RealProcFS struct{}

// Status reads /proc/<pid>/status for PPid and Name (comm).
func (RealProcFS) Status(pid int) (Status, error) {
	path := "/proc/" + strconv.Itoa(pid) + "/status"
	f, err := os.Open(path) //nolint:gosec // path is built from an int pid, not attacker input
	if err != nil {
		return Status{}, fmt.Errorf("ident: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var st Status
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "Name:"):
			st.Name = strings.TrimSpace(strings.TrimPrefix(line, "Name:"))
		case strings.HasPrefix(line, "PPid:"):
			v, convErr := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "PPid:")))
			if convErr != nil {
				return Status{}, fmt.Errorf("ident: parse PPid in %s: %w", path, convErr)
			}
			st.PPid = v
		}
	}
	if err := scanner.Err(); err != nil {
		return Status{}, fmt.Errorf("ident: read %s: %w", path, err)
	}
	return st, nil
}

// Cwd resolves the /proc/<pid>/cwd symlink.
func (RealProcFS) Cwd(pid int) (string, error) {
	path := "/proc/" + strconv.Itoa(pid) + "/cwd"
	cwd, err := os.Readlink(path) //nolint:gosec // path is built from an int pid, not attacker input
	if err != nil {
		return "", fmt.Errorf("ident: readlink %s: %w", path, err)
	}
	return cwd, nil
}

// Cmdline reads /proc/<pid>/cmdline, splitting on the NUL argument separator.
func (RealProcFS) Cmdline(pid int) ([]string, error) {
	path := "/proc/" + strconv.Itoa(pid) + "/cmdline"
	b, err := os.ReadFile(path) //nolint:gosec // path is built from an int pid, not attacker input
	if err != nil {
		return nil, fmt.Errorf("ident: read %s: %w", path, err)
	}
	trimmed := strings.TrimRight(string(b), "\x00")
	if trimmed == "" {
		return nil, nil
	}
	return strings.Split(trimmed, "\x00"), nil
}
