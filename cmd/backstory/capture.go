package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// runCapture is the `backstory capture off|on|status` subcommand. off and
// on write and remove the exact same capture-off flag file
// captureOffPath() names — the one path hook.go's captureOff() (honoured by
// `backstory hook post-tool-use`) and the daemon's own write paths (note,
// status) all read, so there is exactly one source of truth for "is capture
// paused" (AGENT-CONTRACT.md §User-only powers, SCHEMA.md invariant 8).
func runCapture(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "backstory capture: usage: backstory capture off|on|status")
		return 2
	}
	switch args[0] {
	case "off":
		return runCaptureOff(stdout, stderr)
	case "on":
		return runCaptureOn(stdout, stderr)
	case "status":
		return runCaptureStatus(stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "backstory capture: unknown subcommand %q\n\nusage: backstory capture off|on|status\n", args[0])
		return 2
	}
}

// runCaptureOff creates the capture-off flag file. It is idempotent: an
// already-off capture stays off and still exits 0.
func runCaptureOff(stdout, stderr io.Writer) int {
	path, err := captureOffPath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory capture off:", err)
		return 1
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory capture off:", err)
		return 1
	}
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory capture off:", err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, "capture: off")
	return 0
}

// runCaptureOn removes the capture-off flag file. It is idempotent: an
// already-on capture (no flag file present) still exits 0.
func runCaptureOn(stdout, stderr io.Writer) int {
	path, err := captureOffPath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory capture on:", err)
		return 1
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		_, _ = fmt.Fprintln(stderr, "backstory capture on:", err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, "capture: on")
	return 0
}

// runCaptureStatus prints the capture-off flag file's current state. It
// reads the flag file directly rather than dialing the daemon: capture
// on/off is a human-only, filesystem-level power (AGENT-CONTRACT.md
// §User-only powers) that must be inspectable even with the daemon down —
// exactly like `backstory hook post-tool-use` checking it without a socket
// round trip.
func runCaptureStatus(stdout, stderr io.Writer) int {
	off, err := captureOff()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory capture status:", err)
		return 1
	}
	state := "on"
	if off {
		state = "off"
	}
	_, _ = fmt.Fprintln(stdout, "capture:", state)
	return 0
}
