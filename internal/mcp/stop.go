package mcp

import (
	"encoding/json"

	"github.com/RidgetopAi/backstory/internal/store"
)

// DaemonMethodStopCheck is the Stop hook's daemon-side method. Like
// DaemonMethodPostToolUse it is exported because cmd/backstory's `hook stop`
// builds a DaemonRequest for it directly. It is read-only: the daemon
// reports what this connection's session did and never authors a handoff
// (AGENT-CONTRACT.md §The never-list).
const DaemonMethodStopCheck = "stop_check"

// StopCheckResult is stop_check's return value: the observed counts for the
// caller's own session, from which the hook decides whether to ask for a
// handoff.
type StopCheckResult struct {
	EditedFiles    int  `json:"edited_files"`
	FailedCommands int  `json:"failed_commands"`
	HandoffWritten bool `json:"handoff_written"`
}

// handleStopCheck reads sessionID's evidence — the connection's own
// daemon-minted session, never anything the request claims. It refuses while
// capture is off, like every other handler that touches session data.
func handleStopCheck(st *store.Store, sessionID string, captureOff func() (bool, error)) DaemonResponse {
	if off, err := captureOff(); err != nil {
		return errResponse("internal", err.Error())
	} else if off {
		return errResponse("capture-off", "capture is paused; nothing is observed")
	}
	ev, err := st.SessionWorkEvidence(sessionID)
	if err != nil {
		return errResponse("internal", err.Error())
	}
	result, err := json.Marshal(StopCheckResult{
		EditedFiles: ev.EditedFiles, FailedCommands: ev.FailedCommands, HandoffWritten: ev.HandoffWritten,
	})
	if err != nil {
		return errResponse("internal", err.Error())
	}
	return DaemonResponse{Result: result}
}
