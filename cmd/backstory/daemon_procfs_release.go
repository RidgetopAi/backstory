//go:build !backstorytest

package main

import "github.com/RidgetopAi/backstory/internal/ident"

// procFSForDaemon always returns ident.RealProcFS{} in a release build (any
// build without -tags backstorytest, including plain `go build`/`make
// build`). It never reads an environment variable and never mentions
// BACKSTORY_TEST_FAKE_ANCESTRY — that name, and the code that honors it,
// live only in daemon_procfs_backstorytest.go, which is excluded from this
// build. Before this split (task fe7aee40), a single untagged
// procFSForDaemon read that env var unconditionally, so any process able to
// set the daemon's environment could dictate which session/agent a hook
// connection resolved to even in a release binary.
func procFSForDaemon() (ident.ProcFS, error) {
	return ident.RealProcFS{}, nil
}
