//go:build integration

package ident_test

import (
	"os"
	"testing"

	"github.com/RidgetopAi/backstory/internal/ident"
)

// TestRealProcFSCurrentProcess is the one integration-tagged exception to
// "no real /proc in tests": it proves RealProcFS actually reads /proc for
// the running test process. It never runs under `make check`, only
// `make integration`.
func TestRealProcFSCurrentProcess(t *testing.T) {
	var procfs ident.RealProcFS

	st, err := procfs.Status(os.Getpid())
	if err != nil {
		t.Fatalf("Status(%d): %v", os.Getpid(), err)
	}
	if st.Name == "" {
		t.Fatalf("Status(%d).Name is empty", os.Getpid())
	}

	if _, err := procfs.Cwd(os.Getpid()); err != nil {
		t.Fatalf("Cwd(%d): %v", os.Getpid(), err)
	}
	if _, err := procfs.Cmdline(os.Getpid()); err != nil {
		t.Fatalf("Cmdline(%d): %v", os.Getpid(), err)
	}
}
