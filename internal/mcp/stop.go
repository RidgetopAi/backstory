package mcp

import (
	"encoding/json"
	"time"

	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/project"
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
func handleStopCheck(st *store.Store, git project.Git, sessionID string, captureOff func() (bool, error)) DaemonResponse {
	if off, err := captureOff(); err != nil {
		return errResponse("internal", err.Error())
	} else if off {
		return errResponse("capture-off", "capture is paused; nothing is observed")
	}
	ev, err := st.SessionWorkEvidence(sessionID)
	if err != nil {
		return errResponse("internal", err.Error())
	}
	// Edits made through Bash (heredocs, sed) leave no mutating tool event, so
	// the tool rule alone misses them; the repo's observed change since the
	// session started is evidence too. The larger count wins: the two count
	// the same edits in different vocabularies (absolute tool paths vs
	// repo-relative git paths), so adding them would double count.
	if n := gitChangedSinceStart(st, git, sessionID); n > ev.EditedFiles {
		ev.EditedFiles = n
	}
	result, err := json.Marshal(StopCheckResult{
		EditedFiles: ev.EditedFiles, FailedCommands: ev.FailedCommands, HandoffWritten: ev.HandoffWritten,
	})
	if err != nil {
		return errResponse("internal", err.Error())
	}
	return DaemonResponse{Result: result}
}

// gitChangedSinceStart is how many paths the session's own cwd changed
// between the session-start git observation and now: the symmetric
// difference of the two uncommitted-path sets plus the paths in both whose
// content hash changed, and at least 1 when HEAD
// moved (a commit made during the session). It is 0 — the tool rule alone
// decides — when there is no start observation, either observation is
// could-not-observe, or nothing differs.
func gitChangedSinceStart(st *store.Store, git project.Git, sessionID string) int {
	events, err := st.EventsForSessionTimeline(sessionID, time.Time{}, payload.KindSessionGitState, 0)
	if err != nil {
		return 0
	}
	var start *payload.SessionGitState
	for _, e := range events {
		var gs payload.SessionGitState
		if json.Unmarshal([]byte(e.Payload), &gs) == nil && gs.Phase == payload.GitStatePhaseStart {
			start = &gs
			break
		}
	}
	if start == nil || start.CouldNotObserve {
		return 0
	}
	cwd, err := st.SessionCWD(sessionID)
	if err != nil {
		return 0
	}
	now := observeGitState(git, cwd, "")
	if now.CouldNotObserve {
		return 0
	}
	// A path counts when it is in only one of the two sets, or — with a content
	// fingerprint on both sides — in both but with a different hash (an
	// already-dirty file edited again). Without both fingerprints (an older
	// start observation) only the path sets are compared.
	fingerprinted := start.Hashes != nil && now.Hashes != nil
	before := map[string]bool{}
	for _, p := range start.Paths {
		before[p] = true
	}
	after := map[string]bool{}
	n := 0
	for _, p := range now.Paths {
		after[p] = true
		if !before[p] || (fingerprinted && start.Hashes[p] != now.Hashes[p]) {
			n++
		}
	}
	for _, p := range start.Paths {
		if !after[p] {
			n++
		}
	}
	if n == 0 && start.Head != now.Head {
		n = 1
	}
	return n
}
