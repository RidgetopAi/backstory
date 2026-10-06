// Agents adapter (decision 3e14db82): the generic fallback for any
// MCP-capable harness. `backstory install agents` registers `backstory mcp`
// (stdio) in ~/.agents/mcp.json and adds the one-line stub to
// ~/.config/AGENTS.md. No hooks: a generic harness has no known hook surface.
package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Agents item names, for --check output.
const (
	ItemAgentsMCPServer = "mcp-server"
	ItemAgentsMD        = "agents-md-stub"
)

// AgentsStubLine is the one-line AGENTS.md stub.
const AgentsStubLine = "Backstory: call the `backstory` MCP tool's `recall` at the start of a session to load prior context." + HandoffClause

var agentsStubBlock = StubMarkerBegin + "\n" + AgentsStubLine + "\n" + StubMarkerEnd + "\n"

// ErrMalformedAgentsMCPJSON is returned for an unparseable ~/.agents/mcp.json.
var ErrMalformedAgentsMCPJSON = errors.New("install: ~/.agents/mcp.json is not valid JSON")

// AgentsPaths locates the files the agents adapter touches.
type AgentsPaths struct {
	MCPJSON  string
	AgentsMD string
}

// DefaultAgentsPaths returns the generic-harness surfaces under home.
func DefaultAgentsPaths(home string) AgentsPaths {
	return AgentsPaths{
		MCPJSON:  filepath.Join(home, ".agents", "mcp.json"),
		AgentsMD: filepath.Join(home, ".config", "AGENTS.md"),
	}
}

type agentsAdapter struct{}

func (agentsAdapter) Name() string { return HarnessAgents }

func (agentsAdapter) Install(home string, opts Options) error {
	return InstallAgents(DefaultAgentsPaths(home), opts)
}

func (agentsAdapter) Remove(home string, opts Options) error {
	return RemoveAgents(DefaultAgentsPaths(home), opts)
}

func (agentsAdapter) Check(home string, opts Options) ([]Item, error) {
	return CheckAgents(DefaultAgentsPaths(home), opts)
}

func agentsServerValue(opts Options) map[string]any {
	return map[string]any{"command": opts.binaryPath(), "args": []any{"mcp"}}
}

// agentsServerStatus reports mcpServers.backstory: absent, present (exactly
// what we write), or foreign (anything else, including a non-object
// mcpServers).
func agentsServerStatus(root map[string]any, opts Options) ItemStatus {
	raw, ok := root["mcpServers"]
	if !ok {
		return StatusAbsent
	}
	servers, ok := raw.(map[string]any)
	if !ok {
		return StatusForeign
	}
	entry, ok := servers[MCPServerName]
	if !ok {
		return StatusAbsent
	}
	if jsonDeepEqual(entry, agentsServerValue(opts)) {
		return StatusPresent
	}
	if agentsServerIsOurs(entry, opts) {
		return StatusAbsent // our own older entry (bare name or another binary path): install rewrites it
	}
	return StatusForeign
}

// agentsServerIsOurs reports whether entry is `{command: <a binary named
// backstory>, args: ["mcp"]}` and nothing else.
func agentsServerIsOurs(entry any, opts Options) bool {
	m, ok := entry.(map[string]any)
	if !ok || len(m) != 2 {
		return false
	}
	cmd, _ := m["command"].(string)
	return (filepath.Base(cmd) == fallbackBinaryName || cmd == opts.binaryPath()) && jsonDeepEqual(m["args"], []any{"mcp"})
}

// InstallAgents installs both items. A malformed mcp.json fails before
// anything is written, and so does a foreign mcpServers.backstory (reported as
// ErrForeignConflict), so a conflict leaves both files exactly as they were.
func InstallAgents(paths AgentsPaths, opts Options) error {
	root, mode, err := loadJSONObject(paths.MCPJSON, ErrMalformedAgentsMCPJSON)
	if err != nil {
		return err
	}
	if agentsServerStatus(root, opts) == StatusForeign {
		return fmt.Errorf("%w: %s: %s already defines mcpServers.%s; left untouched", ErrForeignConflict, ItemAgentsMCPServer, paths.MCPJSON, MCPServerName)
	}
	if agentsServerStatus(root, opts) == StatusAbsent {
		servers, err := objectField(root, "mcpServers")
		if err != nil {
			return err
		}
		servers[MCPServerName] = agentsServerValue(opts)
		root["mcpServers"] = servers
		if err := writeJSONAtomic(paths.MCPJSON, root, mode); err != nil {
			return fmt.Errorf("%s: %w", ItemAgentsMCPServer, err)
		}
	}
	if err := installStubBlock(paths.AgentsMD, agentsStubBlock); err != nil {
		return fmt.Errorf("%s: %w", ItemAgentsMD, err)
	}
	return nil
}

// RemoveAgents reverses InstallAgents, leaving foreign content untouched. A
// mcp.json that held only our entry is deleted (it did not exist before).
func RemoveAgents(paths AgentsPaths, opts Options) error {
	root, mode, err := loadJSONObject(paths.MCPJSON, ErrMalformedAgentsMCPJSON)
	if err != nil {
		return err
	}
	if st := agentsServerStatus(root, opts); st == StatusPresent || (st == StatusAbsent && agentsServerIsOurs(servers0(root), opts)) {
		servers, _ := root["mcpServers"].(map[string]any)
		delete(servers, MCPServerName)
		if len(servers) == 0 {
			delete(root, "mcpServers")
		}
		if len(root) == 0 {
			if err := os.Remove(paths.MCPJSON); err != nil {
				return fmt.Errorf("%s: %w", ItemAgentsMCPServer, err)
			}
		} else if err := writeJSONAtomic(paths.MCPJSON, root, mode); err != nil {
			return fmt.Errorf("%s: %w", ItemAgentsMCPServer, err)
		}
	}
	if err := removeStub(paths.AgentsMD); err != nil {
		return fmt.Errorf("%s: %w", ItemAgentsMD, err)
	}
	return nil
}

// CheckAgents reports every item's status without writing.
func CheckAgents(paths AgentsPaths, opts Options) ([]Item, error) {
	root, _, err := loadJSONObject(paths.MCPJSON, ErrMalformedAgentsMCPJSON)
	if err != nil {
		return nil, err
	}
	return []Item{
		{Name: ItemAgentsMCPServer, Status: agentsServerStatus(root, opts)},
		{Name: ItemAgentsMD, Status: stubBlockStatus(paths.AgentsMD, agentsStubBlock)},
	}, nil
}

// servers0 returns root's mcpServers.backstory entry, nil when absent.
func servers0(root map[string]any) any {
	servers, _ := root["mcpServers"].(map[string]any)
	return servers[MCPServerName]
}
