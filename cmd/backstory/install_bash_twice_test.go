package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RidgetopAi/backstory/internal/install"
)

// TestInstallBashTwiceSaysAddedThenAlreadyInstalled (task df6646f5).
func TestInstallBashTwiceSaysAddedThenAlreadyInstalled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	run := func() string {
		var out, errb bytes.Buffer
		if code := runInstallBash(nil, &out, &errb); code != 0 {
			t.Fatalf("exit %d: %s", code, errb.String())
		}
		return out.String()
	}
	if first := run(); !strings.Contains(first, "block added") {
		t.Errorf("first install output = %q, want \"block added\"", first)
	}
	second := run()
	if !strings.Contains(second, "already installed") || strings.Contains(second, "block added") {
		t.Errorf("second install output = %q, want \"already installed\"", second)
	}
	data, err := os.ReadFile(filepath.Join(home, ".bashrc")) //nolint:gosec // t.TempDir path
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(data), install.BashrcMarkerComment); n != 1 {
		t.Errorf(".bashrc holds %d blocks, want 1", n)
	}
}
