//go:build backstorytest

package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/RidgetopAi/backstory/internal/ident"
)

// fakeAncestryEnvVar names an env var carrying a JSON-encoded
// []ident.FakeAncestryHop that, when set, replaces RealProcFS with an
// ident.AnchoredFakeProcFS built from it for both the resolver's ancestry
// walk and the block handler's liveness checks. It exists purely so
// cmd/backstory's own tests can pin exactly what /proc ancestry a hook
// connection resolves to, instead of resolving identity against the REAL
// /proc of whatever process happens to be running `go test` (task
// fe2cff2a — see AnchoredFakeProcFS's doc comment for the bug this closes).
//
// This file, and therefore this constant and the env-reading behavior
// below, only compiles into a binary built with -tags backstorytest — a
// release build (plain `go build`/`make build`) gets
// daemon_procfs_release.go instead, whose procFSForDaemon never reads any
// env var and always returns ident.RealProcFS{}. Before this split (task
// fe7aee40), procFSForDaemon lived unconditionally in daemon.go, so any
// process able to set the daemon's environment could dictate which
// session/agent a hook connection resolved to in a RELEASE binary — a test
// hook masquerading as a production identity override.
const fakeAncestryEnvVar = "BACKSTORY_TEST_FAKE_ANCESTRY"

// procFSForDaemon returns ident.RealProcFS{} unless fakeAncestryEnvVar is
// set, in which case it parses the var's JSON []ident.FakeAncestryHop and
// returns an *ident.AnchoredFakeProcFS built from it. The SAME instance
// must back both the resolver and the block/session-sweep liveness checks
// so a walk's minted synthetic ancestor pids stay resolvable across both
// uses within one daemon process.
func procFSForDaemon() (ident.ProcFS, error) {
	raw := os.Getenv(fakeAncestryEnvVar)
	if raw == "" {
		return ident.RealProcFS{}, nil
	}
	var hops []ident.FakeAncestryHop
	if err := json.Unmarshal([]byte(raw), &hops); err != nil {
		return nil, fmt.Errorf("parse %s: %w", fakeAncestryEnvVar, err)
	}
	return &ident.AnchoredFakeProcFS{Hops: hops}, nil
}
