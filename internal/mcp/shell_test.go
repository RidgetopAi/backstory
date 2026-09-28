package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/payload"
)

func TestShellEmitRecordsCommandEventWithCmdCwdExitDuration(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")

	params, err := json.Marshal(ShellEmitParams{Cmd: "sleep 0.2", CWD: "/home/brian/proj", Exit: 0, DurationMS: 204})
	if err != nil {
		t.Fatalf("marshal ShellEmitParams: %v", err)
	}

	shim := dialShim(t, sockPath)
	raw, rerr := shim.callDaemon(DaemonMethodShellEmit, params)
	if rerr != nil {
		t.Fatalf("shell_emit: %v", rerr)
	}
	var result ShellEmitResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal ShellEmitResult: %v", err)
	}
	if result.EventsRecorded != 1 {
		t.Errorf("EventsRecorded = %d, want 1", result.EventsRecorded)
	}

	rows, err := st.DB().Query(`SELECT kind, source, payload FROM timeline_events`)
	if err != nil {
		t.Fatalf("query timeline_events: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var count int
	for rows.Next() {
		count++
		var kind, source, payloadStr string
		if err := rows.Scan(&kind, &source, &payloadStr); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if kind != payload.KindShellCommand {
			t.Errorf("kind = %q, want %q", kind, payload.KindShellCommand)
		}
		if source != "shell" {
			t.Errorf("source = %q, want %q", source, "shell")
		}
		var sc payload.ShellCommand
		if err := json.Unmarshal([]byte(payloadStr), &sc); err != nil {
			t.Fatalf("unmarshal payload %q: %v", payloadStr, err)
		}
		if sc.Cmd != "sleep 0.2" {
			t.Errorf("Cmd = %q, want %q", sc.Cmd, "sleep 0.2")
		}
		if sc.CWD != "/home/brian/proj" {
			t.Errorf("CWD = %q, want %q", sc.CWD, "/home/brian/proj")
		}
		if sc.Exit != 0 {
			t.Errorf("Exit = %d, want 0", sc.Exit)
		}
		if sc.DurationMS != 204 {
			t.Errorf("DurationMS = %d, want 204", sc.DurationMS)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("timeline_events rows = %d, want 1", count)
	}
}

// TestShellEmitCaptureOffInsertsNoEventsCaptureOnInsertsOne is DONE WHEN
// clause 4's capture-off half, mirroring
// TestPostToolUseCaptureOffInsertsNoEventsCaptureOnInsertsOne (hook_test.go):
// shell_emit honours captureOff exactly like post_tool_use and note, even
// though the bash snippet itself never checks the flag client-side at all.
//
// RA-MUTATION-PROBE: delete handleShellEmit's captureOff check -> RED (the
// capture-off call below succeeds and inserts a row instead of being
// refused); restored -> GREEN.
func TestShellEmitCaptureOffInsertsNoEventsCaptureOnInsertsOne(t *testing.T) {
	st := mustOpenStore(t)
	var capture toggleCapture
	capture.off.Store(true)
	sockPath := testDaemonWithCapture(t, st, "claude", "/home/brian/proj", "proj-key", capture.fn)

	params, err := json.Marshal(ShellEmitParams{Cmd: "true", Exit: 0})
	if err != nil {
		t.Fatalf("marshal ShellEmitParams: %v", err)
	}

	if got := countTimelineEvents(t, st); got != 0 {
		t.Fatalf("timeline_events before any request = %d, want 0", got)
	}

	shim := dialShim(t, sockPath)
	_, rerr := shim.callDaemon(DaemonMethodShellEmit, params)
	if rerr == nil {
		t.Fatal("shell_emit with capture off succeeded, want a capture-off error")
	}
	if !strings.Contains(rerr.Message, "capture") {
		t.Errorf("error message = %q, want it to mention capture", rerr.Message)
	}
	if got := countTimelineEvents(t, st); got != 0 {
		t.Fatalf("timeline_events after shell_emit with capture off = %d, want 0", got)
	}

	capture.off.Store(false)
	shim2 := dialShim(t, sockPath)
	if _, rerr := shim2.callDaemon(DaemonMethodShellEmit, params); rerr != nil {
		t.Fatalf("shell_emit with capture on: %v", rerr)
	}
	if got := countTimelineEvents(t, st); got != 1 {
		t.Fatalf("timeline_events after shell_emit with capture on = %d, want 1", got)
	}
}

// TestShellEmitMissingCmdIsInvalidParams guards the one required field:
// Cmd empty is rejected before anything is written, distinct from the
// leading-space "quiet skip" case below.
func TestShellEmitMissingCmdIsInvalidParams(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")

	params, err := json.Marshal(ShellEmitParams{Exit: 0})
	if err != nil {
		t.Fatalf("marshal ShellEmitParams: %v", err)
	}

	shim := dialShim(t, sockPath)
	_, rerr := shim.callDaemon(DaemonMethodShellEmit, params)
	if rerr == nil {
		t.Fatal("shell_emit with no cmd succeeded, want invalid-params")
	}
	if got := countTimelineEvents(t, st); got != 0 {
		t.Fatalf("timeline_events after a missing-cmd request = %d, want 0", got)
	}
}

// TestShellEmitLeadingSpaceCommandRecordsNothing is DONE WHEN clause 4's
// leading-space half, enforced server-side (handleShellEmit's own doc
// comment explains why: shell_emit is reachable from any socket peer, not
// just Backstory's own bash snippet). A quiet skip: no error, but
// EventsRecorded is 0 and nothing lands in timeline_events.
//
// RA-MUTATION-PROBE: delete handleShellEmit's `strings.HasPrefix(p.Cmd, " ")`
// check -> RED (the space-prefixed command below is stored); restored ->
// GREEN.
func TestShellEmitLeadingSpaceCommandRecordsNothing(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")

	params, err := json.Marshal(ShellEmitParams{Cmd: " rm -rf /tmp/whatever", Exit: 0})
	if err != nil {
		t.Fatalf("marshal ShellEmitParams: %v", err)
	}

	shim := dialShim(t, sockPath)
	raw, rerr := shim.callDaemon(DaemonMethodShellEmit, params)
	if rerr != nil {
		t.Fatalf("shell_emit with a leading-space command: %v", rerr)
	}
	var result ShellEmitResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal ShellEmitResult: %v", err)
	}
	if result.EventsRecorded != 0 {
		t.Errorf("EventsRecorded = %d, want 0 for a leading-space command", result.EventsRecorded)
	}
	if got := countTimelineEvents(t, st); got != 0 {
		t.Fatalf("timeline_events after a leading-space command = %d, want 0", got)
	}
}

// TestShellEmitSecretShapedCommandStoredRedacted is DONE WHEN clause 4's
// redaction half: AppendEvent's generic redact() (store/redact.go) applies
// to a shell command's payload exactly like it does to every other event
// payload — this test pins that specifically for payload.ShellCommand's own
// "cmd" field, rather than relying only on the generic case already covered
// by store/redact_test.go.
func TestShellEmitSecretShapedCommandStoredRedacted(t *testing.T) {
	st := mustOpenStore(t)
	sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")

	const secretCmd = `curl -H "Authorization: Bearer abcdef0123456789.secret-token" https://example.invalid` //nolint:gosec // a fake bearer token this test feeds through redaction, not a real credential
	params, err := json.Marshal(ShellEmitParams{Cmd: secretCmd, Exit: 0})
	if err != nil {
		t.Fatalf("marshal ShellEmitParams: %v", err)
	}

	shim := dialShim(t, sockPath)
	if _, rerr := shim.callDaemon(DaemonMethodShellEmit, params); rerr != nil {
		t.Fatalf("shell_emit: %v", rerr)
	}

	var storedPayload string
	if err := st.DB().QueryRow(`SELECT payload FROM timeline_events WHERE kind = ?`, payload.KindShellCommand).Scan(&storedPayload); err != nil {
		t.Fatalf("query stored payload: %v", err)
	}
	if strings.Contains(storedPayload, "abcdef0123456789.secret-token") {
		t.Errorf("stored payload still contains the raw secret: %q", storedPayload)
	}
	if !strings.Contains(storedPayload, "[redacted:bearer-token]") {
		t.Errorf("stored payload = %q, want it to contain the redaction marker", storedPayload)
	}
}

// TestShellEmitProjectComesFromResolverNotParams is DONE WHEN clause 5: two
// dials against daemons resolving DIFFERENT observed identities (distinct
// harness/cwd/projectKey fixtures), given the IDENTICAL shell_emit params,
// land in their own respective projects — the event's session (and so its
// project) comes from the connection's own resolved identity alone, never
// anything shell_emit's params carry (there is no session/harness/project
// field in ShellEmitParams for a hostile or buggy caller to even try).
func TestShellEmitProjectComesFromResolverNotParams(t *testing.T) {
	stA := mustOpenStore(t)
	sockA := testDaemon(t, stA, "claude", "/home/brian/proj-a", "proj-a-key")
	stB := mustOpenStore(t)
	sockB := testDaemon(t, stB, "claude", "/home/brian/proj-b", "proj-b-key")

	params, err := json.Marshal(ShellEmitParams{Cmd: "true", Exit: 0})
	if err != nil {
		t.Fatalf("marshal ShellEmitParams: %v", err)
	}

	shimA := dialShim(t, sockA)
	if _, rerr := shimA.callDaemon(DaemonMethodShellEmit, params); rerr != nil {
		t.Fatalf("shell_emit against daemon A: %v", rerr)
	}
	shimB := dialShim(t, sockB)
	if _, rerr := shimB.callDaemon(DaemonMethodShellEmit, params); rerr != nil {
		t.Fatalf("shell_emit against daemon B: %v", rerr)
	}

	var cwdA, cwdB string
	if err := stA.DB().QueryRow(`SELECT cwd FROM sessions`).Scan(&cwdA); err != nil {
		t.Fatalf("query session cwd on store A: %v", err)
	}
	if err := stB.DB().QueryRow(`SELECT cwd FROM sessions`).Scan(&cwdB); err != nil {
		t.Fatalf("query session cwd on store B: %v", err)
	}
	if cwdA != "/home/brian/proj-a" {
		t.Errorf("store A session cwd = %q, want %q (the resolver's observed cwd)", cwdA, "/home/brian/proj-a")
	}
	if cwdB != "/home/brian/proj-b" {
		t.Errorf("store B session cwd = %q, want %q (the resolver's observed cwd)", cwdB, "/home/brian/proj-b")
	}
}
