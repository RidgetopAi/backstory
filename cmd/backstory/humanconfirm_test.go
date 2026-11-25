package main

import "testing"

// TestAgentSessionDetectsEveryTableMarker drives agentSession over each
// agentSessionEnv row alone, the empty environment, and a marker set to the
// empty string, so a row dropped from the table, or a detector that ignores
// it, is caught here rather than only for CLAUDECODE by the subprocess tests.
func TestAgentSessionDetectsEveryTableMarker(t *testing.T) {
	if len(agentSessionEnv) == 0 {
		t.Fatal("agentSessionEnv is empty: nothing would ever be detected")
	}
	if m, ok := agentSession(func(string) string { return "" }); ok {
		t.Fatalf("empty environment detected as an agent session via %s", m.Var)
	}
	for _, want := range agentSessionEnv {
		env := map[string]string{want.Var: "1"}
		got, ok := agentSession(func(k string) string { return env[k] })
		if !ok || got.Var != want.Var {
			t.Errorf("%s=1 alone: agentSession = (%q, %v), want (%q, true)", want.Var, got.Var, ok, want.Var)
		}
		if want.Harness == "" || want.Source == "" {
			t.Errorf("%s: Harness and Source must both be filled in", want.Var)
		}
	}
	empty := map[string]string{"CLAUDECODE": ""}
	if _, ok := agentSession(func(k string) string { return empty[k] }); ok {
		t.Error("CLAUDECODE set to the empty string detected as an agent session, want only a non-empty value to count")
	}
}
