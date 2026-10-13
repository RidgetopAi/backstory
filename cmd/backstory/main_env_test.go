package main

import (
	"os"
	"testing"
)

// TestMain clears BACKSTORY_PROJECT_ROOTS from the invoking environment so no
// test (in-process or a subprocess inheriting os.Environ) is scoped by the
// developer's own setting; tests that want roots set them explicitly.
func TestMain(m *testing.M) {
	_ = os.Unsetenv("BACKSTORY_PROJECT_ROOTS")
	os.Exit(m.Run())
}
