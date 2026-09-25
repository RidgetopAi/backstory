package mcp

import (
	"encoding/json"

	"github.com/RidgetopAi/backstory/internal/block"
	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// BlockResult is DaemonMethodBlock's return value: the rendered
// SessionStart block text (internal/block), for the hook subcommand to
// print verbatim.
type BlockResult struct {
	Block string `json:"block"`
}

// handleBlock renders the SessionStart block for the caller's own project,
// exactly as the daemon observed it (id.ProjectKey) — never from anything a
// request line claims — excluding the caller's own session from slot 3's
// coordination list.
func handleBlock(st *store.Store, procfs ident.ProcFS, id ident.Identity, sessionID string, git project.Git) DaemonResponse {
	text, err := block.Render(block.Params{
		Store:      st,
		ProcFS:     procfs,
		ProjectKey: id.ProjectKey,
		SessionID:  sessionID,
		Harness:    id.Harness,
		CWD:        id.CWD,
		Git:        git,
	})
	if err != nil {
		return errResponse("internal", err.Error())
	}
	result, err := json.Marshal(BlockResult{Block: text})
	if err != nil {
		return errResponse("internal", err.Error())
	}
	return DaemonResponse{Result: result}
}
