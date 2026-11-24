package install

import "testing"

// The commands install itself writes, and the ones earlier releases wrote,
// are still recognised: a plain path, the bare name, a single-quoted path.
func TestOurHookCommandForms(t *testing.T) {
	opts := Options{BinaryPath: "/elsewhere/bs"}
	ours := map[string]string{
		"/usr/local/bin/backstory " + subPostToolUse:           "/usr/local/bin/backstory",
		"backstory " + subPostToolUse:                          "backstory",
		"'/opt/my dir/backstory' " + subPostToolUse:            "/opt/my dir/backstory",
		`'/opt/it'\''s/backstory' ` + subPostToolUse:           "/opt/it's/backstory",
		"/elsewhere/bs " + subPostToolUse:                      "/elsewhere/bs",
		"'/opt/X=1/backstory' " + subPostToolUse:               "/opt/X=1/backstory",
		Options{BinaryPath: "X=1"}.hookCommand(subPostToolUse): "X=1",
	}
	for cmd, bin := range ours {
		got, ok := ourHookBinary(cmd, subPostToolUse, Options{BinaryPath: bin})
		if !ok || got != bin {
			t.Errorf("ourHookBinary(%q) = %q, %v; want %q, true", cmd, got, ok, bin)
		}
		if bin != "X=1" && bin != "/elsewhere/bs" {
			if _, ok := ourHookBinary(cmd, subPostToolUse, opts); !ok {
				t.Errorf("%q not recognised under another --binary", cmd)
			}
		}
	}
	notOurs := []string{
		subPostToolUse,
		" " + subPostToolUse,
		"/usr/bin/other " + subPostToolUse,
		"backstory " + subPostToolUseFailure,
		"''" + " " + subPostToolUse,
		"'/opt/a' '/opt/backstory' " + subPostToolUse,
		"'/opt/unterminated/backstory " + subPostToolUse,
	}
	for name, cmd := range ownerWrappers(subPostToolUse) {
		notOurs = append(notOurs, cmd)
		_ = name
	}
	for _, cmd := range notOurs {
		if bin, ok := ourHookBinary(cmd, subPostToolUse, opts); ok {
			t.Errorf("ourHookBinary(%q) = %q, true; want not ours", cmd, bin)
		}
	}
}
