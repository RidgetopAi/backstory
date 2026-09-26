package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/RidgetopAi/backstory/internal/ident"
)

// buildBackstoryNoTags compiles the backstory binary with NO build tags —
// what `make build` and every release artifact actually ship — so tests in
// this file can prove that binary behaves differently from buildBackstory's
// -tags backstorytest one (daemon_test.go) with respect to
// BACKSTORY_TEST_FAKE_ANCESTRY.
func buildBackstoryNoTags(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "backstory-release")
	cmd := exec.Command("go", "build", "-o", bin, ".") //nolint:gosec // bin is a t.TempDir() path this test built, not external input
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build backstory (no tags): %v\n%s", err, out)
	}
	return bin
}

// TestReleaseBuildIgnoresFakeAncestryEnvVar is task fe7aee40's DONE WHEN
// clause 1: a daemon built WITHOUT -tags backstorytest must ignore
// BACKSTORY_TEST_FAKE_ANCESTRY entirely and resolve identity from the REAL
// /proc ancestry, while the exact same env var against a daemon built WITH
// -tags backstorytest resolves the fake agent. Before this split (a single
// untagged procFSForDaemon reading the env var unconditionally), any
// process able to set the daemon's environment could dictate which
// session/agent a hook connection resolved to even in a release binary — a
// test-only override masquerading as production identity.
//
// The injected fake harness is "codex", not "claude": this test suite may
// itself be running under a real "claude" ancestor, so asserting merely
// "resolved agent is not codex" stays true regardless of the real ancestry
// this test happens to run under, while still being a positive, checkable
// fact about whether the env var was honored.
func TestReleaseBuildIgnoresFakeAncestryEnvVar(t *testing.T) {
	const fakeHarness = "codex"
	fakeAncestry := []ident.FakeAncestryHop{{Name: fakeHarness}}

	t.Run("release build ignores the env var", func(t *testing.T) {
		bin := buildBackstoryNoTags(t)
		dbPath, _, env := startTestDaemon(t, bin, fakeAncestry...)

		_, stderr, exitCode := runHookSubprocess(t, bin, env, map[string]any{
			"session_id":      "release-ignores-fake-ancestry",
			"cwd":             "/home/brian/proj",
			"transcript_path": "/home/brian/.claude/projects/proj/x.jsonl",
			"source":          "startup",
			"hook_event_name": "SessionStart",
		})
		if exitCode != 0 {
			t.Fatalf("exit code = %d, want 0 (stderr: %s)", exitCode, stderr)
		}

		s := mustOpenTestStore(t, dbPath)
		row := querySessionByHarnessSessionID(t, s, "release-ignores-fake-ancestry")
		if row.Agent == fakeHarness {
			t.Fatalf("agent = %q, want anything but %q: a release build honored BACKSTORY_TEST_FAKE_ANCESTRY, letting a test-only env var dictate production identity", row.Agent, fakeHarness)
		}
	})

	t.Run("backstorytest build honors the env var", func(t *testing.T) {
		bin := buildBackstory(t) // daemon_test.go's helper: -tags backstorytest
		dbPath, _, env := startTestDaemon(t, bin, fakeAncestry...)

		_, stderr, exitCode := runHookSubprocess(t, bin, env, map[string]any{
			"session_id":      "backstorytest-honors-fake-ancestry",
			"cwd":             "/home/brian/proj",
			"transcript_path": "/home/brian/.claude/projects/proj/x.jsonl",
			"source":          "startup",
			"hook_event_name": "SessionStart",
		})
		if exitCode != 0 {
			t.Fatalf("exit code = %d, want 0 (stderr: %s)", exitCode, stderr)
		}

		s := mustOpenTestStore(t, dbPath)
		row := querySessionByHarnessSessionID(t, s, "backstorytest-honors-fake-ancestry")
		if row.Agent != fakeHarness {
			t.Fatalf("agent = %q, want %q: a -tags backstorytest build must still honor BACKSTORY_TEST_FAKE_ANCESTRY (every existing fake-ancestry test depends on this)", row.Agent, fakeHarness)
		}
	})
}

// TestReleaseBuildBinaryDoesNotContainFakeAncestryEnvVarName is task
// fe7aee40's DONE WHEN clause 2: `go build ./cmd/backstory` with no build
// tags must produce a binary that never mentions
// BACKSTORY_TEST_FAKE_ANCESTRY — the constant lives only in
// daemon_procfs_backstorytest.go, which -tags backstorytest excludes from
// this build, so the string cannot be baked into the binary's string table
// even as dead data an operator might stumble on.
func TestReleaseBuildBinaryDoesNotContainFakeAncestryEnvVarName(t *testing.T) {
	bin := buildBackstoryNoTags(t)
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatalf("read release binary: %v", err)
	}
	if needle := []byte("BACKSTORY_TEST_FAKE_ANCESTRY"); bytes.Contains(data, needle) {
		t.Fatalf("release binary contains %q: a test-only identity override leaked into the production build", needle)
	}
}
