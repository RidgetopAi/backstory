package store

import (
	"regexp"
	"strings"
)

// secretPattern matches one class of secret the store must never persist
// (AGENT-CONTRACT.md §The never-list, item 7). kind names the replacement
// so a redacted record still says what was scrubbed, never what it was.
type secretPattern struct {
	kind string
	re   *regexp.Regexp
	// keep makes the replacement group 1 (kept verbatim) plus the marker.
	// accept, when non-nil, vetoes a match that is really prose.
	keep   bool
	accept func(m []string) bool
}

var secretPatterns = []secretPattern{
	{kind: "bearer-token", re: regexp.MustCompile(`(?i)\bBearer\s+([A-Za-z0-9._~+/=-]{8,})`),
		// A real token carries a digit or punctuation; a bare word such as
		// "Authorization" is prose.
		accept: func(m []string) bool { return strings.ContainsAny(m[1], "0123456789._~+/=-") }},
	{kind: "api-key", re: regexp.MustCompile(`\bsk-(?:ant|proj|svcacct|admin)-[A-Za-z0-9_-]{8,}`)},
	{kind: "api-key", re: regexp.MustCompile(`\bsk-[A-Za-z0-9]{16,}\b`)},
	{kind: "github-token", re: regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})`)},
	{kind: "slack-token", re: regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`)},
	{kind: "jwt", re: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)},
	{kind: "aws-key", re: regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{kind: "pem-block", re: regexp.MustCompile(`(?s)-----BEGIN [A-Z ]+-----.*?-----END [A-Z ]+-----`)},
	{kind: "password", re: regexp.MustCompile(`(?i)\bpassword\s*=\s*\S+`)},
	// Env-style assignment: NAME ends in KEY/TOKEN/SECRET/PASSWORD. The name
	// (group 1, with the "=" and any opening quote) survives; the value goes.
	{kind: "env-secret", keep: true,
		re: regexp.MustCompile(`(?i)(\b[A-Za-z0-9_]*(?:KEY|TOKEN|SECRET|PASSWORD)\s*=\s*(?:\\?["'])?)([^\s"'\\]+)`),
		// Values an earlier pattern already scrubbed stay as they are.
		accept: func(m []string) bool { return !strings.HasPrefix(m[2], "[redacted:") }},
}

// Redact replaces every secret pattern match in text with
// "[redacted:<kind>]". Text with no match is returned unchanged.
func Redact(text string) string {
	for _, p := range secretPatterns {
		marker := "[redacted:" + p.kind + "]"
		if p.accept == nil && !p.keep {
			text = p.re.ReplaceAllString(text, marker)
			continue
		}
		text = p.re.ReplaceAllStringFunc(text, func(match string) string {
			m := p.re.FindStringSubmatch(match)
			if p.accept != nil && !p.accept(m) {
				return match
			}
			if p.keep {
				return m[1] + marker
			}
			return marker
		})
	}
	return text
}
