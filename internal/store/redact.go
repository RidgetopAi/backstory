package store

import "regexp"

// secretPattern matches one class of secret the store must never persist
// (AGENT-CONTRACT.md §The never-list, item 7). kind names the replacement
// so a redacted record still says what was scrubbed, never what it was.
type secretPattern struct {
	kind string
	re   *regexp.Regexp
}

var secretPatterns = []secretPattern{
	{"bearer-token", regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{8,}`)},
	{"api-key", regexp.MustCompile(`\bsk-[A-Za-z0-9]{16,}\b`)},
	{"aws-key", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{"pem-block", regexp.MustCompile(`(?s)-----BEGIN [A-Z ]+-----.*?-----END [A-Z ]+-----`)},
	{"password", regexp.MustCompile(`(?i)\bpassword\s*=\s*\S+`)},
}

// redact replaces every secret pattern match in text with
// "[redacted:<kind>]". Text with no match is returned unchanged.
func redact(text string) string {
	for _, p := range secretPatterns {
		text = p.re.ReplaceAllString(text, "[redacted:"+p.kind+"]")
	}
	return text
}
