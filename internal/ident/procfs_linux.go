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

// Status reads /proc/<pid>/status for PPid and Name (comm), plus
// /proc/<pid>/stat for StartTicks.
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

	st.StartTicks, st.SID, err = readStat(pid)
	if err != nil {
		return Status{}, err
	}
	return st, nil
}

// readStat reads /proc/<pid>/stat's field 22 (starttime) and field 6 (session). The comm
// field (field 2) is parenthesized and may itself contain spaces or
// parens, so the field split anchors on the LAST ")" in the line rather
// than counting from the front — the same trick ps/procps use.
func readStat(pid int) (uint64, int, error) {
	path := "/proc/" + strconv.Itoa(pid) + "/stat"
	b, err := os.ReadFile(path) //nolint:gosec // path is built from an int pid, not attacker input
	if err != nil {
		return 0, 0, fmt.Errorf("ident: read %s: %w", path, err)
	}
	s := string(b)
	close := strings.LastIndexByte(s, ')')
	if close == -1 || close+2 > len(s) {
		return 0, 0, fmt.Errorf("ident: parse %s: no comm field", path)
	}
	// fields[0] is state (stat field 3); starttime (stat field 22) is
	// therefore fields[22-3] = fields[19].
	fields := strings.Fields(s[close+2:])
	const startTimeField = 19
	// session (stat field 6) is fields[6-3].
	const sessionField = 3
	if len(fields) <= startTimeField {
		return 0, 0, fmt.Errorf("ident: parse %s: too few fields after comm", path)
	}
	v, err := strconv.ParseUint(fields[startTimeField], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("ident: parse starttime in %s: %w", path, err)
	}
	sid, err := strconv.Atoi(fields[sessionField])
	if err != nil {
		return 0, 0, fmt.Errorf("ident: parse session in %s: %w", path, err)
	}
	return v, sid, nil
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
