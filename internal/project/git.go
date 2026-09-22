package project

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
)

// RealGit shells out to the git binary. It is the production Git
// implementation; tests use a fake (key_test.go).
type RealGit struct{}

// Repo implements Git by running git rev-parse / git remote in cwd.
func (RealGit) Repo(cwd string) (Repo, bool) {
	common, ok := gitOutput(cwd, "rev-parse", "--git-common-dir")
	if !ok || common == "" {
		return Repo{}, false
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(cwd, common)
	}
	common = filepath.Clean(common)

	top, _ := gitOutput(cwd, "rev-parse", "--show-toplevel")
	remote := firstRemoteURL(cwd)

	return Repo{CommonDir: common, RemoteURL: remote, Toplevel: top}, true
}

// firstRemoteURL returns the URL of the first remote `git remote` lists for
// cwd's repo, or "" if none is configured.
func firstRemoteURL(cwd string) string {
	out, ok := gitOutput(cwd, "remote")
	if !ok || out == "" {
		return ""
	}
	names := strings.Fields(out)
	if len(names) == 0 {
		return ""
	}
	url, ok := gitOutput(cwd, "remote", "get-url", names[0])
	if !ok {
		return ""
	}
	return url
}

// gitOutput runs git with args in cwd and returns its trimmed stdout. ok is
// false on any non-zero exit (typically: cwd is not a git working tree).
func gitOutput(cwd string, args ...string) (string, bool) {
	cmd := exec.Command("git", args...) //nolint:gosec // args are a fixed set of literal git subcommands, never caller-controlled
	cmd.Dir = cwd
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", false
	}
	return strings.TrimSpace(out.String()), true
}
