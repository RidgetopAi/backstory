package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/RidgetopAi/backstory/internal/mcp"
)

// stopPayload is a Claude Code Stop hook's JSON payload. StopHookActive is
// Claude's own once-only guard: true when this Stop is already the
// continuation a previous Stop hook's block caused. The other fields are
// accepted so a real payload decodes cleanly; nothing reads them — the
// session is resolved from the connection's observed identity like every
// other hook.
type stopPayload struct {
	SessionID      string `json:"session_id"`
	CWD            string `json:"cwd"`
	TranscriptPath string `json:"transcript_path"`
	HookEventName  string `json:"hook_event_name"`
	StopHookActive bool   `json:"stop_hook_active"`
}

// stopBlockDecision is Claude's Stop-hook block shape: the reason is fed
// back to the model, which keeps working instead of stopping.
type stopBlockDecision struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

const stopDecisionBlock = "block"

// stopReason is the one-time nudge, built only from observed counts. The
// failed-command clause appears only when there were failures.
func stopReason(r mcp.StopCheckResult) string {
	msg := fmt.Sprintf("edited %d file(s)", r.EditedFiles)
	if r.FailedCommands > 0 {
		msg += fmt.Sprintf(", %d failed command(s)", r.FailedCommands)
	}
	return msg + ", wrote no handoff — note handoff, or say in one line why none is needed."
}

// runStopHook is `backstory hook stop`. When this session changed a file or
// had a failed command, wrote no handoff, and Claude says the Stop is not
// already a continuation, it prints one block decision asking for a handoff.
// Every other path — read-only session, handoff written, stop_hook_active,
// BACKSTORY_NO_SESSION, capture off, no daemon, any daemon or decode error —
// prints nothing to stdout and exits 0: Backstory must never block a user on
// its own failure. Only Claude has a Stop event shaped this way, so a Codex
// payload is also silent.
func runStopHook(stdin io.Reader, stdout, stderr io.Writer, harness string) int {
	if os.Getenv(noSessionEnv) != "" || harness == harnessCodex {
		return 0
	}
	if off, err := captureOff(); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory hook: check capture-off flag:", err)
		return 0
	} else if off {
		return 0
	}
	var p stopPayload
	if err := json.NewDecoder(stdin).Decode(&p); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory hook: decode stop payload:", err)
		return 0
	}
	if p.StopHookActive {
		return 0
	}
	resp, err := callDaemon(p.SessionID, mcp.DaemonMethodStopCheck, nil)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory hook:", err)
		return 0
	}
	var result mcp.StopCheckResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory hook: decode stop_check result:", err)
		return 0
	}
	if result.HandoffWritten || (result.EditedFiles == 0 && result.FailedCommands == 0) {
		return 0
	}
	b, err := json.Marshal(stopBlockDecision{Decision: stopDecisionBlock, Reason: stopReason(result)})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory hook: marshal stop decision:", err)
		return 0
	}
	_, _ = fmt.Fprintln(stdout, string(b))
	return 0
}
