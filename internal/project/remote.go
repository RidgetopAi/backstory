package project

import (
	"net/url"
	"strings"
)

// gitSuffix is the conventional suffix of a bare repository URL.
const gitSuffix = ".git"

// caseInsensitiveForges are the hosts whose repository paths are
// case-insensitive, so NormalizeRemote lower-cases the path for them. Every
// other host keeps path case: on a self-hosted forge `Org/App` and `org/app`
// may be different repositories, and sync must never guess.
var caseInsensitiveForges = map[string]bool{
	"github.com":   true,
	"gitlab.com":   true,
	"codeberg.org": true,
}

// remoteSchemes are the URL schemes NormalizeRemote accepts. file:// and any
// other scheme name no machine-independent repository, so they never match.
var remoteSchemes = map[string]bool{
	"http": true, "https": true, "ssh": true, "git": true, "git+ssh": true, "ssh+git": true,
}

// NormalizeRemote turns a git remote URL into a machine-independent
// repository id of the form "host/path": the host is lower-cased and the
// scheme, user@, port, a trailing ".git" and trailing slashes are dropped;
// scp form (user@host:path) is mapped to path form. Path case is kept unless
// the host is a known case-insensitive forge. It returns false for an empty
// or unparseable URL and for a local path or file:// remote: those never
// identify a repository across machines.
func NormalizeRemote(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	var host, path string
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || !remoteSchemes[strings.ToLower(u.Scheme)] {
			return "", false
		}
		host, path = u.Hostname(), u.Path
	} else {
		// scp form: [user@]host:path. A colon must precede any slash, else
		// this is a local path.
		colon := strings.Index(raw, ":")
		if colon <= 0 || strings.Contains(raw[:colon], "/") {
			return "", false
		}
		host, path = raw[:colon], raw[colon+1:]
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		if len(host) == 1 { // a Windows drive letter, not a host
			return "", false
		}
	}
	host = strings.ToLower(host)
	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, gitSuffix)
	path = strings.Trim(path, "/")
	if host == "" || path == "" || strings.ContainsAny(host+path, " \t\n\\") {
		return "", false
	}
	if caseInsensitiveForges[host] {
		path = strings.ToLower(path)
	}
	return host + "/" + path, true
}
