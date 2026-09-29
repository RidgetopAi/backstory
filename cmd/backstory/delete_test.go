package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// seedDeletableRecord opens a fresh store at dbPath (creating it, same as
// storePath()/store.Open would from a `backstory delete` invocation),
// upserts projectKey (records.project_key is a real foreign key into
// projects), and inserts one record, returning its id.
func seedDeletableRecord(t *testing.T, dbPath, projectKey, text string) string {
	t.Helper()
	st, err := store.Open(dbPath, nil, nil)
	if err != nil {
		t.Fatalf("store.Open(%s): %v", dbPath, err)
	}
	defer func() { _ = st.Close() }()

	if err := st.UpsertProject(store.Project{Key: projectKey, Toplevel: "/tmp/" + projectKey, FirstSeen: time.Now()}); err != nil {
		t.Fatalf("UpsertProject(%s): %v", projectKey, err)
	}

	id, err := st.InsertRecord(store.InsertRecordParams{
		Identity:   store.Identity{Kind: store.IdentityAgent, Actor: "seed"},
		Kind:       store.KindNote,
		Text:       text,
		ProjectKey: projectKey,
	})
	if err != nil {
		t.Fatalf("InsertRecord: %v", err)
	}
	return id
}

// mustGetRecord opens dbPath and reads back id, failing the test if either
// step errors.
func mustGetRecord(t *testing.T, dbPath, id string) store.Record {
	t.Helper()
	st, err := store.Open(dbPath, nil, nil)
	if err != nil {
		t.Fatalf("store.Open(%s): %v", dbPath, err)
	}
	defer func() { _ = st.Close() }()

	rec, err := st.GetRecord(id)
	if err != nil {
		t.Fatalf("GetRecord(%s): %v", id, err)
	}
	return rec
}

// TestDeleteWithYesTombstonesRecordRowStillExists is the punch's DONE WHEN
// clause 1's success path: `backstory delete <id> --yes` against a temp
// store sets the record's tombstoned_at and scrubs its text and leaves the row
// itself in place (SCHEMA.md invariant 1: a tombstoned record still exists).
func TestDeleteWithYesTombstonesRecordRowStillExists(t *testing.T) {
	bin := buildBackstory(t)
	dataDir := t.TempDir()
	env := testXDGEnv("XDG_DATA_HOME=" + dataDir)
	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")

	id := seedDeletableRecord(t, dbPath, "proj-delete-yes", "delete me\nsecond line kept for context")

	cmd := exec.Command(bin, "delete", id, "--yes") //nolint:gosec // bin is the binary this test just built, id is this test's own seeded uuid
	cmd.Env = env
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err != nil {
		t.Fatalf("backstory delete --yes: %v (stdout: %s, stderr: %s)", err, out.String(), errOut.String())
	}

	rec := mustGetRecord(t, dbPath, id)
	if rec.TombstonedAt == nil {
		t.Fatalf("record %s TombstonedAt is nil after delete --yes, want it set", id)
	}
	if rec.Text != "" {
		t.Errorf("record %s Text = %q after delete --yes, want it scrubbed to '' (the row itself remains)", id, rec.Text)
	}
}

// TestDeleteWithoutYesOnNonTTYRefusesAndLeavesRecordUntouched is the punch's
// DONE WHEN clause 1's refusal path: without --yes, on a non-interactive
// stdin (an explicit bytes.Reader — never a real terminal, exactly like
// every other subprocess test in this package feeds its child's stdin),
// `backstory delete <id>` exits non-zero and never tombstones the record.
func TestDeleteWithoutYesOnNonTTYRefusesAndLeavesRecordUntouched(t *testing.T) {
	bin := buildBackstory(t)
	dataDir := t.TempDir()
	env := testXDGEnv("XDG_DATA_HOME=" + dataDir)
	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")

	id := seedDeletableRecord(t, dbPath, "proj-delete-refuse", "must survive an unconfirmed delete")

	cmd := exec.Command(bin, "delete", id) //nolint:gosec // bin is the binary this test just built, id is this test's own seeded uuid
	cmd.Env = env
	cmd.Stdin = bytes.NewReader(nil) // explicit non-*os.File reader: never a terminal
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	if err == nil {
		t.Fatalf("backstory delete with no --yes on a non-TTY exited 0, want non-zero (stdout: %s)", out.String())
	}
	if errOut.Len() == 0 {
		t.Errorf("stderr is empty, want a message explaining the refusal")
	}

	rec := mustGetRecord(t, dbPath, id)
	if rec.TombstonedAt != nil {
		t.Fatalf("record %s TombstonedAt = %v after a refused delete, want nil (untouched)", id, *rec.TombstonedAt)
	}
}

// TestDeleteUnknownIDFailsWithoutTouchingStore covers the id-not-found path
// with --yes set, so the refusal-path assertion above is not the only
// reason runDelete can exit non-zero.
func TestDeleteUnknownIDFailsWithoutTouchingStore(t *testing.T) {
	bin := buildBackstory(t)
	dataDir := t.TempDir()
	env := testXDGEnv("XDG_DATA_HOME=" + dataDir)
	dbPath := filepath.Join(dataDir, "backstory", "backstory.db")

	// Force store creation (and its directory) without seeding a record, so
	// store.Open in the subprocess opens an existing, empty database.
	seedID := seedDeletableRecord(t, dbPath, "proj-delete-unknown", "unrelated seeded record")

	cmd := exec.Command(bin, "delete", "does-not-exist", "--yes") //nolint:gosec // bin is the binary this test just built, fixed literal id
	cmd.Env = env
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if err := cmd.Run(); err == nil {
		t.Fatalf("backstory delete of an unknown id exited 0, want non-zero (stdout: %s)", out.String())
	}

	rec := mustGetRecord(t, dbPath, seedID)
	if rec.TombstonedAt != nil {
		t.Errorf("unrelated record %s was tombstoned by a delete of an unknown id", seedID)
	}
}
