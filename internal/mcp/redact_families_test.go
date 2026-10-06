package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/testsecrets"
)

// TestNoteAndShellEmitRedactEveryCredentialFamily: each family's fake value
// is absent from records.text after a note and from timeline_events.payload
// after a shell emit; an env-style line keeps its variable name.
func TestNoteAndShellEmitRedactEveryCredentialFamily(t *testing.T) {
	for _, f := range testsecrets.All() {
		t.Run(f.Family, func(t *testing.T) {
			st := mustOpenStore(t)
			sockPath := testDaemon(t, st, "claude", "/home/brian/proj", "proj-key")
			shim := dialShim(t, sockPath)

			noteArgs, _ := json.Marshal(map[string]string{"kind": "note", "text": f.Text})
			raw, rerr := shim.CallTool(ToolNote, noteArgs)
			if rerr != nil {
				t.Fatalf("note: %v", rerr)
			}
			var res NoteResult
			if err := json.Unmarshal(raw, &res); err != nil {
				t.Fatal(err)
			}
			rec, err := st.GetRecord(res.ID)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(rec.Text, f.Secret) {
				t.Errorf("records.text carries the secret: %q", rec.Text)
			}
			if f.Keep != "" && !strings.Contains(rec.Text, f.Keep) {
				t.Errorf("records.text lost the variable name %q: %q", f.Keep, rec.Text)
			}

			params, _ := json.Marshal(ShellEmitParams{Cmd: f.Text, Exit: 0})
			if _, rerr := shim.callDaemon(DaemonMethodShellEmit, params); rerr != nil {
				t.Fatalf("shell_emit: %v", rerr)
			}
			var stored string
			if err := st.DB().QueryRow(`SELECT payload FROM timeline_events WHERE kind = ?`, payload.KindShellCommand).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(stored, f.Secret) {
				t.Errorf("shell payload carries the secret: %q", stored)
			}
			if f.Keep != "" && !strings.Contains(stored, f.Keep) {
				t.Errorf("shell payload lost the variable name %q: %q", f.Keep, stored)
			}
		})
	}
}
