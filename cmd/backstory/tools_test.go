package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/RidgetopAi/backstory/internal/mcp"
)

// TestToolsJSONMatchesToolsV0 compares the CLI output to ToolsV0() itself —
// never to a literal list — so the panel's help pane cannot drift from the
// tools the agent is actually offered.
func TestToolsJSONMatchesToolsV0(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"tools", "--json"}, nil, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut.String())
	}
	var got struct {
		Tools []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode %q: %v", out.String(), err)
	}
	want := mcp.ToolsV0()
	if len(want) == 0 {
		t.Fatal("ToolsV0() is empty")
	}
	if len(got.Tools) != len(want) {
		t.Fatalf("got %d tools, want %d", len(got.Tools), len(want))
	}
	for i, w := range want {
		if got.Tools[i].Name != w.Name || got.Tools[i].Description != w.Description {
			t.Errorf("tool %d: got %+v, want name %q description %q", i, got.Tools[i], w.Name, w.Description)
		}
	}
}

func TestToolsRequiresJSONFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"tools"}, nil, &out, &errOut); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}
