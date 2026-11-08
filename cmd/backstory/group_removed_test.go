package main

import (
	"strings"
	"testing"
)

// Project groups were removed (decision 8b726bd2): `backstory group` is an
// unknown command and the usage no longer lists it.
func TestGroupCommandIsGone(t *testing.T) {
	var out, errb strings.Builder
	code := run([]string{"group", "list"}, strings.NewReader(""), &out, &errb)
	if code == 0 {
		t.Fatalf("`backstory group list` exited 0, want non-zero (stdout %q)", out.String())
	}
	if !strings.Contains(errb.String(), "unknown command") {
		t.Errorf("stderr = %q, want it to say unknown command", errb.String())
	}

	for _, args := range [][]string{{}, {"help"}} {
		out.Reset()
		errb.Reset()
		run(args, strings.NewReader(""), &out, &errb)
		if text := out.String() + errb.String(); !strings.Contains(text, "this-week") {
			t.Fatalf("run(%v) printed no usage: %q", args, text)
		} else if strings.Contains(strings.ToLower(text), "group") {
			t.Errorf("run(%v) usage still mentions group:\n%s", args, text)
		}
	}
}
