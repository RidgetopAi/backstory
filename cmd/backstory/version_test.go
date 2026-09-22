package main

import (
	"bytes"
	"encoding/json"
	"runtime"
	"testing"

	"github.com/RidgetopAi/backstory/internal/version"
)

func TestVersionPlain(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	want := version.Version + "\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestVersionJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"version", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	if bytes.Count(stdout.Bytes(), []byte("\n")) != 1 {
		t.Fatalf("stdout = %q, want exactly one line", stdout.String())
	}

	var got map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout.String(), err)
	}

	want := map[string]string{
		"version": version.Version,
		"go":      runtime.Version(),
		"os":      runtime.GOOS,
		"arch":    runtime.GOARCH,
	}

	if len(got) != len(want) {
		t.Fatalf("json object has %d keys %v, want exactly %d keys %v", len(got), got, len(want), want)
	}
	for k, wantV := range want {
		gotV, ok := got[k]
		if !ok {
			t.Fatalf("json object missing key %q: %v", k, got)
		}
		if gotV != wantV {
			t.Fatalf("key %q = %v, want %q", k, gotV, wantV)
		}
	}
}
