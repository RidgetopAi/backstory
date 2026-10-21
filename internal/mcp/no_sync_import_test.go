package mcp

import (
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/ident"
	"github.com/RidgetopAi/backstory/internal/store"
)

// TestSocketAPIExposesNoSyncImport pins AGENT-CONTRACT.md's never-list: a
// synced record is imported only by the human-run sync from a paired device.
// No daemon method and no agent tool imports.
func TestSocketAPIExposesNoSyncImport(t *testing.T) {
	for _, method := range []string{"import", "sync", "sync_import", "import_sync_changes", "push", "sync_push", "sync-serve", "pull"} {
		resp := dispatchDaemonRequest([]byte(`{"method":"`+method+`","params":{"changes":[]}}`), nil, nil,
			store.Identity{Kind: store.IdentityAgent}, "", ident.Identity{Kind: ident.KindAgent}, nil, nil, nil)
		if resp.Error == nil || resp.Error.Code != "unknown-method" {
			t.Errorf("method %q: response %+v, want unknown-method", method, resp)
		}
	}
	for _, tool := range ToolsV0() {
		n := strings.ToLower(tool.Name + " " + tool.Description)
		if strings.Contains(n, "sync") || strings.Contains(n, "import") {
			t.Errorf("tool %s mentions sync/import", tool.Name)
		}
	}
}
