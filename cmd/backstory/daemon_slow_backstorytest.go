//go:build backstorytest

package main

import (
	"os"
	"strconv"
	"time"
)

// slowHandlerEnvVar names an env var holding a number of milliseconds each
// accepted connection's handler sleeps before serving it. It exists so the
// SIGTERM test can keep a request genuinely in flight when the signal lands.
// Like BACKSTORY_TEST_FAKE_ANCESTRY, it only compiles into a build made with
// -tags backstorytest; daemon_slow_release.go is the release counterpart.
const slowHandlerEnvVar = "BACKSTORY_TEST_SLOW_HANDLER_MS"

func testSlowHandler() {
	if ms, err := strconv.Atoi(os.Getenv(slowHandlerEnvVar)); err == nil && ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
}
