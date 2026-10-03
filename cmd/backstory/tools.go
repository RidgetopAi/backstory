package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/RidgetopAi/backstory/internal/mcp"
)

// toolSummary is one entry of `backstory tools --json`: the name and the
// human description of an agent tool, nothing of its argument schema.
type toolSummary struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// runTools is `backstory tools --json`: it prints every tool in
// mcp.ToolsV0(), in order, as {"tools":[{name,description}...]}. The panel's
// help pane renders this instead of carrying its own copy of the tool list.
func runTools(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("tools", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "print the agent tools' names and descriptions as JSON")
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !*jsonOut {
		_, _ = fmt.Fprintln(stderr, "usage: backstory tools --json")
		return 2
	}
	tools := mcp.ToolsV0()
	out := struct {
		Tools []toolSummary `json:"tools"`
	}{Tools: make([]toolSummary, 0, len(tools))}
	for _, t := range tools {
		out.Tools = append(out.Tools, toolSummary{Name: t.Name, Description: t.Description})
	}
	if err := json.NewEncoder(stdout).Encode(out); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory tools:", err)
		return 1
	}
	return 0
}
