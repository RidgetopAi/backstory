package mcp

import (
	"encoding/json"
	"time"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/store"
)

// settingCaptureOn is the settings key status reads for "capture on/off"
// (AGENT-CONTRACT.md §The five tools). Absent means on: capture-off is an
// explicit human action (AGENT-CONTRACT.md §User-only powers), never the
// default.
const settingCaptureOn = "capture_on"

// LiveSession is one entry of status's other_live_sessions list.
type LiveSession struct {
	Session   string `json:"session"`
	Harness   string `json:"harness"`
	StartedAt string `json:"started_at"`
}

// StatusResult is status's return value (AGENT-CONTRACT.md §The five
// tools): who the caller is as the daemon observed them, who else is live
// in the same project, and the caller's remaining write budget.
type StatusResult struct {
	Kind              string        `json:"kind"`
	Harness           string        `json:"harness"`
	ProjectKey        string        `json:"project_key"`
	Session           string        `json:"session"`
	OtherLiveSessions []LiveSession `json:"other_live_sessions"`
	RemainingBudget   int           `json:"remaining_budget"`
	CaptureOn         bool          `json:"capture_on"`
	// Reason mirrors ident.Identity.Reason: non-empty only when a harness
	// was found but its cwd could not be read (task f2718b5b), so a caller
	// can tell that apart from "no harness found" instead of seeing the
	// same empty ProjectKey either way.
	Reason string `json:"reason,omitempty"`
}

// handleStatus never writes to the store; it only reads sessions, the
// caller's own rate-cap usage, and the capture-on setting.
func handleStatus(st *store.Store, id ident.Identity, sessionID string) DaemonResponse {
	remaining, err := st.RemainingWriteBudget(sessionID)
	if err != nil {
		return errResponse("internal", err.Error())
	}

	others := []LiveSession{}
	if id.ProjectKey != "" {
		sessions, err := st.LiveSessionsInProject(id.ProjectKey)
		if err != nil {
			return errResponse("internal", err.Error())
		}
		for _, s := range sessions {
			if s.ID == sessionID {
				continue
			}
			others = append(others, LiveSession{
				Session:   s.ID,
				Harness:   s.Agent,
				StartedAt: s.StartedAt.UTC().Format(time.RFC3339),
			})
		}
	}

	captureOn := true
	if v, ok, err := st.GetSetting(settingCaptureOn); err != nil {
		return errResponse("internal", err.Error())
	} else if ok {
		captureOn = v != "false"
	}

	result, err := json.Marshal(StatusResult{
		Kind:              id.Kind.String(),
		Harness:           id.Harness,
		ProjectKey:        id.ProjectKey,
		Session:           sessionID,
		OtherLiveSessions: others,
		RemainingBudget:   remaining,
		CaptureOn:         captureOn,
		Reason:            id.Reason,
	})
	if err != nil {
		return errResponse("internal", err.Error())
	}
	return DaemonResponse{Result: result}
}
