package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// linkNoneLabel is what `link list` shows for a project with no sync identity.
const linkNoneLabel = "-"

// runLink dispatches `backstory link set|clear|list`.
func runLink(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "backstory link: usage: backstory link set|clear|list")
		return 2
	}
	switch args[0] {
	case "set":
		return runLinkSet(args[1:], stdout, stderr)
	case "clear":
		return runLinkClear(args[1:], stdout, stderr)
	case "list":
		return runLinkList(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "backstory link: unknown subcommand %q\n\nusage: backstory link set|clear|list\n", args[0])
		return 2
	}
}

// runLinkSet is `backstory link set <name> [<project-key>|--here]`. It writes through humanIdentity: a link is a human-only power.
func runLinkSet(args []string, stdout, stderr io.Writer) int {
	const usage = "usage: backstory link set <name> [<project-key>|--here]"
	positional, here, err := splitHereArgs(args, usage)
	if err == nil && (here || len(positional) != 2) && (!here || len(positional) != 1) {
		err = errors.New(usage)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory link set:", err)
		return 2
	}
	name := positional[0]

	st, code := openStoreFor("link set", stderr)
	if st == nil {
		return code
	}
	defer func() { _ = st.Close() }()

	projectKey := ""
	if here {
		if projectKey, err = resolveHereProjectKey(st); err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory link set:", err)
			return 1
		}
	} else {
		projectKey = positional[1]
	}
	if err := st.SetProjectLink(humanIdentity, name, projectKey); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory link set:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "link %s: %s\n", name, projectKey)
	return 0
}

// runLinkClear is `backstory link clear [<project-key>|--here]`.
func runLinkClear(args []string, stdout, stderr io.Writer) int {
	const usage = "usage: backstory link clear [<project-key>|--here]"
	positional, here, err := splitHereArgs(args, usage)
	if err == nil && (here || len(positional) != 1) && (!here || len(positional) != 0) {
		err = errors.New(usage)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory link clear:", err)
		return 2
	}

	st, code := openStoreFor("link clear", stderr)
	if st == nil {
		return code
	}
	defer func() { _ = st.Close() }()

	var projectKey string
	if here {
		if projectKey, err = resolveHereProjectKey(st); err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory link clear:", err)
			return 1
		}
	} else {
		projectKey = positional[0]
	}
	if err := st.ClearProjectLink(humanIdentity, projectKey); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory link clear:", err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, "cleared:", projectKey)
	return 0
}

// linkListRow is one project in `backstory link list --json`.
type linkListRow struct {
	Key    string `json:"key"`
	SyncID string `json:"sync_id"`
	Source string `json:"source"` // "remote", "link", "synced" (placeholder) or "" for none
}

// runLinkList is `backstory link list [--json]`: every project with its sync
// identity (remote-derived, linked or none). Read-only.
func runLinkList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("link list", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "print the projects and their sync identities as JSON")
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "backstory link list: usage: backstory link list [--json]")
		return 2
	}
	st, code := openStoreFor("link list", stderr)
	if st == nil {
		return code
	}
	defer func() { _ = st.Close() }()

	ids, err := st.ListSyncIdentities()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory link list:", err)
		return 1
	}
	rows := make([]linkListRow, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, linkListRow{Key: id.ProjectKey, SyncID: id.ID, Source: string(id.Source)})
	}
	if *jsonOut {
		b, err := json.Marshal(rows)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory link list:", err)
			return 1
		}
		_, _ = fmt.Fprintln(stdout, string(b))
		return 0
	}
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(stdout, "no projects")
		return 0
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "KEY\tSYNC ID\tSOURCE")
	for _, r := range rows {
		syncID, source := r.SyncID, r.Source
		if syncID == "" {
			syncID, source = linkNoneLabel, linkNoneLabel
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Key, syncID, source)
	}
	_ = tw.Flush()
	return 0
}

// splitHereArgs walks args by hand (flag.FlagSet stops at the first
// non-flag, which would misparse "set name --here"), returning the
// positionals and whether --here was given.
func splitHereArgs(args []string, usage string) (positional []string, here bool, err error) {
	for _, a := range args {
		switch {
		case a == "--here" || a == "-here":
			here = true
		case strings.HasPrefix(a, "-"):
			return nil, false, fmt.Errorf("unknown flag %q; %s", a, usage)
		default:
			positional = append(positional, a)
		}
	}
	return positional, here, nil
}

// openStoreFor opens the store for the named subcommand, printing any error
// to stderr; the *store.Store is nil on failure and code is the exit status.
func openStoreFor(sub string, stderr io.Writer) (*store.Store, int) {
	dbPath, err := storePath()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "backstory %s: %v\n", sub, err)
		return nil, 1
	}
	st, err := openStore(dbPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "backstory %s: %v\n", sub, err)
		return nil, 1
	}
	return st, 0
}

// errNotAProject is returned by resolveHereProjectKey when --here is run
// outside any git working tree: AGENT-CONTRACT.md's "Project = git
// repository identity" means a bare directory (a workspace, in the
// original description's word for it) has no project identity to link —
// internal/project.Key would otherwise silently fall back to cwd itself,
// which is not an observed project and must never become one just because
// a human happened to run this command from it.
func errNotAProject(cwd string) error {
	return fmt.Errorf("--here: %s is not inside a git repository; a workspace is not a project", cwd)
}

// resolveHereProjectKey resolves the current directory's project key the
// same way the daemon's session resolver does (project.Key over
// project.RealGit, cmd/backstory/daemon.go's runDaemon), refuses a
// directory outside any git working tree, and upserts the project row
// first — mirroring internal/mcp/daemon.go's startSession, since
// --here may be the first time this project's key is ever written.
func resolveHereProjectKey(st *store.Store) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve cwd: %w", err)
	}
	git := project.RealGit{}
	repo, ok := git.Repo(cwd)
	if !ok {
		return "", errNotAProject(cwd)
	}
	// Same workspace rule as the daemon (decision bcc9fa54); no resolvable
	// home dir just means no default workspace.
	workspaces, _ := project.DefaultWorkspaceDirs()
	key := project.Key(cwd, git, workspaces)
	if err := st.UpsertProject(store.Project{
		Key:          key,
		GitCommonDir: repo.CommonDir,
		RemoteURL:    repo.RemoteURL,
		Toplevel:     repo.Toplevel,
		FirstSeen:    time.Now(),
	}); err != nil {
		return "", err
	}
	return key, nil
}

// resolveWorkspaceDirs resolves the configured workspace directories once,
// here, at the process entry (task 482b2320, decision f3fa04c7's clause 7:
// workspace dirs are resolved by the caller, never internally by
// internal/week or internal/store) — callers need this same list to
// canonicalize a legacy plain key to its workspace-prefixed form. nil (never resolved) is safe: it just means
// no key is ever seen as a workspace legacy form, the same "no workspace
// configured" fallback DefaultWorkspaceDirs' own callers already used.
func resolveWorkspaceDirs() []string {
	workspaceDirs, err := project.DefaultWorkspaceDirs()
	if err != nil {
		return nil
	}
	return workspaceDirs
}
