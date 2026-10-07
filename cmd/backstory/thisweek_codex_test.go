package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/install"
)

// codexApprovalHome builds a fixture HOME. withHooks installs the Backstory
// Codex hooks into ~/.codex/hooks.json; trusted adds a trusted_hash entry for
// each hook key to config.toml. Neither: no Codex installed.
func codexApprovalHome(t *testing.T, withHooks, trusted bool) string {
	t.Helper()
	home := t.TempDir()
	if !withHooks {
		return home
	}
	if err := install.InstallCodex(install.DefaultCodexPaths(home), install.Options{}); err != nil {
		t.Fatal(err)
	}
	if trusted {
		p := install.DefaultCodexPaths(home)
		cfg, err := os.ReadFile(p.ConfigTOML) //nolint:gosec // test fixture path
		if err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		b.Write(cfg)
		for _, ev := range []string{"session_start", "post_tool_use"} {
			fmt.Fprintf(&b, "\n[hooks.state.\"%s:%s:0:0\"]\ntrusted_hash = \"sha256:abc\"\n", p.HooksJSON, ev)
		}
		if err := os.WriteFile(p.ConfigTOML, []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func runThisWeekHome(t *testing.T, home string, args ...string) string {
	t.Helper()
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "data"))
	t.Setenv("HOME", home)
	var out, errBuf bytes.Buffer
	if code := run(append([]string{"this-week"}, args...), bytes.NewReader(nil), &out, &errBuf); code != 0 {
		t.Fatalf("this-week exit %d: %s", code, errBuf.String())
	}
	return out.String()
}

func codexAttention(t *testing.T, jsonOut string) []attentionItemJSON {
	t.Helper()
	var o thisWeekOutputJSON
	if err := json.Unmarshal([]byte(jsonOut), &o); err != nil {
		t.Fatal(err)
	}
	var got []attentionItemJSON
	for _, it := range o.Attention {
		if it.Kind == "codex-hooks-not-approved" {
			got = append(got, it)
		}
	}
	return got
}

func TestThisWeekCodexHooksUnapprovedAttention(t *testing.T) {
	home := codexApprovalHome(t, true, false)

	text := runThisWeekHome(t, home)
	if !strings.Contains(text, "Attention:") || !strings.Contains(text, "not approved") || !strings.Contains(text, "Trust all and continue") {
		t.Errorf("text output lacks the Codex approval Attention item:\n%s", text)
	}
	items := codexAttention(t, runThisWeekHome(t, home, "--json"))
	if len(items) != 1 || !strings.Contains(items[0].Reason, "not approved") || !strings.Contains(items[0].Reason, "Trust all and continue") {
		t.Errorf("json attention = %+v, want one Codex approval item", items)
	}
	if items[0].EvidenceIDs == nil {
		t.Error("evidence_ids must be [], never null")
	}
}

func TestThisWeekNoCodexApprovalItemWhenTrustedOrAbsent(t *testing.T) {
	for name, home := range map[string]string{
		"trusted":         codexApprovalHome(t, true, true),
		"codex-not-found": codexApprovalHome(t, false, false),
	} {
		if text := runThisWeekHome(t, home); strings.Contains(text, "Trust all and continue") {
			t.Errorf("%s: text output has the approval item:\n%s", name, text)
		}
		if items := codexAttention(t, runThisWeekHome(t, home, "--json")); len(items) != 0 {
			t.Errorf("%s: json has approval items: %+v", name, items)
		}
	}
}
