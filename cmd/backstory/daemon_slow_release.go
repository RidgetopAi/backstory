//go:build !backstorytest

package main

// testSlowHandler is a no-op in a release build; the slow path and its env
// var live only in daemon_slow_backstorytest.go.
func testSlowHandler() {}
