package mcp

import (
	"encoding/json"
	"os"
	"testing"
)

// toolsListEnvelope mirrors the tools/list response shape: {"tools": [...]}.
type toolsListEnvelope struct {
	Tools []Tool `json:"tools"`
}

// TestToolsV0MatchesFrozenSnapshotByteForByte is DONE WHEN clause 1: tools/list
// must match testdata/tools-v0.json byte-for-byte after canonicalisation.
func TestToolsV0MatchesFrozenSnapshotByteForByte(t *testing.T) {
	want, err := os.ReadFile("testdata/tools-v0.json")
	if err != nil {
		t.Fatalf("read testdata/tools-v0.json: %v", err)
	}
	wantCanon, err := canonicalizeJSON(want)
	if err != nil {
		t.Fatalf("canonicalize testdata/tools-v0.json: %v", err)
	}

	got, err := json.Marshal(toolsListEnvelope{Tools: ToolsV0()})
	if err != nil {
		t.Fatalf("marshal ToolsV0(): %v", err)
	}
	gotCanon, err := canonicalizeJSON(got)
	if err != nil {
		t.Fatalf("canonicalize ToolsV0(): %v", err)
	}

	if string(gotCanon) != string(wantCanon) {
		t.Fatalf("ToolsV0() does not match testdata/tools-v0.json.\ngot:  %s\nwant: %s", gotCanon, wantCanon)
	}
}

// TestToolsV0ExactlyFiveFrozenNames pins the tool set and order named in
// AGENT-CONTRACT.md §The five tools.
func TestToolsV0ExactlyFiveFrozenNames(t *testing.T) {
	want := []string{ToolRecall, ToolNote, ToolTimeline, ToolConfirm, ToolStatus}
	tools := ToolsV0()
	if len(tools) != len(want) {
		t.Fatalf("ToolsV0() has %d tools, want %d", len(tools), len(want))
	}
	for i, name := range want {
		if tools[i].Name != name {
			t.Errorf("ToolsV0()[%d].Name = %q, want %q", i, tools[i].Name, name)
		}
	}
}

// withMutatedNoteSchema returns a copy of tools with the note tool's
// InputSchema decoded, edited in place by mutate, and re-encoded. Building
// mutations this way (rather than hand-typing a full literal schema per test
// case) means a case can only differ from the frozen schema in the one way
// it says it does.
func withMutatedNoteSchema(t *testing.T, tools []Tool, mutate func(*inputSchemaShape)) []Tool {
	t.Helper()
	out := make([]Tool, len(tools))
	copy(out, tools)
	for i, tl := range out {
		if tl.Name != ToolNote {
			continue
		}
		var shape inputSchemaShape
		if err := json.Unmarshal(tl.InputSchema, &shape); err != nil {
			t.Fatalf("unmarshal note schema: %v", err)
		}
		mutate(&shape)
		b, err := json.Marshal(shape)
		if err != nil {
			t.Fatalf("marshal mutated note schema: %v", err)
		}
		out[i].InputSchema = b
	}
	return out
}

// TestCheckAdditiveOnlyEncodesTheAdditiveOnlyRule is the punch's clause 1
// mutation-probe target: a candidate that removes a field or changes a
// field's type must be rejected (RED without the fix reverted); a candidate
// that only adds a new optional field must be accepted (GREEN).
func TestCheckAdditiveOnlyEncodesTheAdditiveOnlyRule(t *testing.T) {
	frozen := ToolsV0()

	t.Run("identical schema is compatible", func(t *testing.T) {
		if err := CheckAdditiveOnly(frozen, ToolsV0()); err != nil {
			t.Fatalf("CheckAdditiveOnly(frozen, frozen) = %v, want nil", err)
		}
	})

	t.Run("removing an existing field is rejected", func(t *testing.T) {
		mutated := withMutatedNoteSchema(t, frozen, func(s *inputSchemaShape) {
			delete(s.Properties, "text")
		})
		if err := CheckAdditiveOnly(frozen, mutated); err == nil {
			t.Fatal("CheckAdditiveOnly accepted a schema with a removed field (text), want an error")
		}
	})

	t.Run("changing a field's type is rejected", func(t *testing.T) {
		mutated := withMutatedNoteSchema(t, frozen, func(s *inputSchemaShape) {
			s.Properties["text"] = json.RawMessage(`{"type": "integer"}`)
		})
		if err := CheckAdditiveOnly(frozen, mutated); err == nil {
			t.Fatal("CheckAdditiveOnly accepted a schema with text's type changed to integer, want an error")
		}
	})

	t.Run("adding a new optional field is accepted", func(t *testing.T) {
		mutated := withMutatedNoteSchema(t, frozen, func(s *inputSchemaShape) {
			s.Properties["priority"] = json.RawMessage(`{"type": "string", "description": "a brand new optional field"}`)
		})
		if err := CheckAdditiveOnly(frozen, mutated); err != nil {
			t.Fatalf("CheckAdditiveOnly rejected an additive-only change (new optional field): %v", err)
		}
	})

	t.Run("removing a whole tool is rejected", func(t *testing.T) {
		var mutated []Tool
		for _, tl := range frozen {
			if tl.Name != ToolConfirm {
				mutated = append(mutated, tl)
			}
		}
		if err := CheckAdditiveOnly(frozen, mutated); err == nil {
			t.Fatal("CheckAdditiveOnly accepted a candidate missing the confirm tool, want an error")
		}
	})

	t.Run("adding a new required field is rejected", func(t *testing.T) {
		mutated := withMutatedNoteSchema(t, frozen, func(s *inputSchemaShape) {
			s.Required = append(s.Required, "about")
		})
		if err := CheckAdditiveOnly(frozen, mutated); err == nil {
			t.Fatal("CheckAdditiveOnly accepted a new required field (about), want an error")
		}
	})
}
