package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

func runLinkCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb strings.Builder
	code := run(args, strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

func TestLinkSetClearListAndProjectsSyncColumn(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataDir)
	dbPath, err := storePath()
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dbPath, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, remote := range map[string]string{
		"/h/code/app":     "git@github.com:X/App.git",
		"/h/projects/app": "https://github.com/x/app",
		"/h/scratch":      "",
	} {
		if err := st.UpsertProject(store.Project{Key: k, RemoteURL: remote, Toplevel: k, FirstSeen: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.Close()

	listJSON := func() map[string]linkListRow {
		code, out, errs := runLinkCLI(t, "link", "list", "--json")
		if code != 0 {
			t.Fatalf("link list: %d %s", code, errs)
		}
		var rows []linkListRow
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			t.Fatal(err)
		}
		m := map[string]linkListRow{}
		for _, r := range rows {
			m[r.Key] = r
		}
		return m
	}

	m := listJSON()
	if m["/h/code/app"].SyncID != "github.com/x/app" || m["/h/projects/app"].SyncID != "github.com/x/app" || m["/h/scratch"].SyncID != "" {
		t.Fatalf("initial list = %+v", m)
	}

	if code, _, errs := runLinkCLI(t, "link", "set", "notes", "/h/scratch"); code != 0 {
		t.Fatalf("link set: %d %s", code, errs)
	}
	if got := listJSON()["/h/scratch"]; got.SyncID != "notes" || got.Source != "link" {
		t.Fatalf("after set = %+v", got)
	}

	code, out, _ := runLinkCLI(t, "projects", "--json")
	var prows []projectRow
	if code != 0 || json.Unmarshal([]byte(out), &prows) != nil {
		t.Fatalf("projects --json: %d %s", code, out)
	}
	found := false
	for _, r := range prows {
		if r.Key == "/h/scratch" {
			found = r.SyncID == "notes" && r.SyncSource == "link"
		}
	}
	if !found {
		t.Fatalf("projects --json lacks sync identity: %s", out)
	}
	if _, out, _ := runLinkCLI(t, "projects"); !strings.Contains(out, "SYNC") || !strings.Contains(out, "notes") {
		t.Fatalf("projects table lacks SYNC column:\n%s", out)
	}

	if code, _, errs := runLinkCLI(t, "link", "clear", "/h/scratch"); code != 0 {
		t.Fatalf("link clear: %d %s", code, errs)
	}
	if got := listJSON()["/h/scratch"]; got.SyncID != "" {
		t.Fatalf("after clear = %+v", got)
	}
	if code, _, _ := runLinkCLI(t, "link", "set", "onlyname"); code != 2 {
		t.Fatalf("link set with no key: exit %d, want 2", code)
	}
	if code, _, _ := runLinkCLI(t, "link", "bogus"); code != 2 {
		t.Fatalf("link bogus: exit %d, want 2", code)
	}
}
