package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// groupListRecentWindow is how far back `backstory group list` looks for
// "ungrouped projects seen this week" (the original description's own
// phrase for the feature). It is a fixed calendar week, not read from any
// config file — nothing else in this CLI has a config file to put it in —
// but it lives here, named, rather than as a literal duration where it is
// used.
const groupListRecentWindow = 7 * 24 * time.Hour

// runGroup dispatches `backstory group set|clear|list`.
func runGroup(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "backstory group: usage: backstory group set|clear|list")
		return 2
	}
	switch args[0] {
	case "set":
		return runGroupSet(args[1:], stdout, stderr)
	case "clear":
		return runGroupClear(args[1:], stdout, stderr)
	case "list":
		return runGroupList(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "backstory group: unknown subcommand %q\n\nusage: backstory group set|clear|list\n", args[0])
		return 2
	}
}

// runGroupSet is `backstory group set <group> [<project-key>|--here]`. It
// writes through humanIdentity (delete.go): grouping is a human-only power
// (SCHEMA.md, decision bcc9fa54), and this CLI process is the one direct
// route to store.IdentityHuman, the same reasoning runDelete's doc comment
// gives for TombstoneRecord.
func runGroupSet(args []string, stdout, stderr io.Writer) int {
	groupName, projectKey, here, err := parseGroupSetArgs(args)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory group set:", err)
		return 2
	}

	dbPath, err := storePath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory group set:", err)
		return 1
	}
	st, err := store.Open(dbPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory group set:", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	if here {
		projectKey, err = resolveHereProjectKey(st)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory group set:", err)
			return 1
		}
	}

	if err := st.SetProjectGroup(humanIdentity, groupName, projectKey); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory group set:", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "group %s: %s\n", groupName, projectKey)
	return 0
}

// runGroupClear is `backstory group clear [<project-key>|--here]`.
func runGroupClear(args []string, stdout, stderr io.Writer) int {
	projectKey, here, err := parseGroupClearArgs(args)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory group clear:", err)
		return 2
	}

	dbPath, err := storePath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory group clear:", err)
		return 1
	}
	st, err := store.Open(dbPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory group clear:", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	if here {
		projectKey, err = resolveHereProjectKey(st)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory group clear:", err)
			return 1
		}
	}

	if err := st.ClearProjectGroup(humanIdentity, projectKey); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory group clear:", err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, "cleared:", projectKey)
	return 0
}

// groupListEntry is one group's row in `backstory group list --json`.
type groupListEntry struct {
	Group    string   `json:"group"`
	Projects []string `json:"projects"`
}

// groupListOutput is the whole of `backstory group list --json`.
type groupListOutput struct {
	Groups    []groupListEntry `json:"groups"`
	Ungrouped []string         `json:"ungrouped"`
}

// runGroupList is `backstory group list [--json]`: every group with its
// projects, plus the projects seen in the last groupListRecentWindow that
// are in no group at all.
func runGroupList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("group list", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "print groups and ungrouped projects as JSON")
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "backstory group list: usage: backstory group list [--json]")
		return 2
	}

	dbPath, err := storePath()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory group list:", err)
		return 1
	}
	st, err := store.Open(dbPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory group list:", err)
		return 1
	}
	defer func() { _ = st.Close() }()

	memberships, err := st.ListGroups()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory group list:", err)
		return 1
	}
	seen, err := st.ProjectsSeenSince(time.Now().Add(-groupListRecentWindow))
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory group list:", err)
		return 1
	}

	// ListGroups already orders by group_name then project_key, so a single
	// pass preserves that order for both the group list and each group's
	// project list.
	grouped := map[string]bool{}
	byGroup := map[string][]string{}
	var groupOrder []string
	for _, m := range memberships {
		grouped[m.ProjectKey] = true
		if _, ok := byGroup[m.GroupName]; !ok {
			groupOrder = append(groupOrder, m.GroupName)
		}
		byGroup[m.GroupName] = append(byGroup[m.GroupName], m.ProjectKey)
	}

	ungrouped := make([]string, 0, len(seen))
	for _, key := range seen {
		if !grouped[key] {
			ungrouped = append(ungrouped, key)
		}
	}

	if *jsonOut {
		out := groupListOutput{Groups: []groupListEntry{}, Ungrouped: ungrouped}
		for _, g := range groupOrder {
			out.Groups = append(out.Groups, groupListEntry{Group: g, Projects: byGroup[g]})
		}
		b, err := json.Marshal(out)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory group list:", err)
			return 1
		}
		_, _ = fmt.Fprintln(stdout, string(b))
		return 0
	}

	if len(groupOrder) == 0 {
		_, _ = fmt.Fprintln(stdout, "no groups set")
	}
	for _, g := range groupOrder {
		_, _ = fmt.Fprintf(stdout, "%s:\n", g)
		for _, p := range byGroup[g] {
			_, _ = fmt.Fprintf(stdout, "  %s\n", p)
		}
	}
	_, _ = fmt.Fprintln(stdout, "ungrouped (seen this week):")
	if len(ungrouped) == 0 {
		_, _ = fmt.Fprintln(stdout, "  (none)")
	}
	for _, p := range ungrouped {
		_, _ = fmt.Fprintf(stdout, "  %s\n", p)
	}
	return 0
}

// errNotAProject is returned by resolveHereProjectKey when --here is run
// outside any git working tree: AGENT-CONTRACT.md's "Project = git
// repository identity" means a bare directory (a workspace, in the
// original description's word for it) has no project identity to group —
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
// project_groups.project_key foreign-keys into projects and --here may be
// the first time this project's key is ever written.
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
	key := project.Key(cwd, git)
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

// parseGroupSetArgs parses `set <group> [<project-key>|--here]`. Like
// parseDeleteArgs (delete.go), this walks args by hand rather than using
// flag.FlagSet: the standard library's flag parsing stops at the first
// non-flag argument, which would misparse "set work --here" (the group
// name, a non-flag, comes first).
func parseGroupSetArgs(args []string) (groupName, projectKey string, here bool, err error) {
	const usage = "usage: backstory group set <group> [<project-key>|--here]"
	var positional []string
	for _, a := range args {
		switch {
		case a == "--here" || a == "-here":
			here = true
		case strings.HasPrefix(a, "-"):
			return "", "", false, fmt.Errorf("unknown flag %q; %s", a, usage)
		default:
			positional = append(positional, a)
		}
	}
	switch {
	case here && len(positional) == 1:
		return positional[0], "", true, nil
	case !here && len(positional) == 2:
		return positional[0], positional[1], false, nil
	default:
		return "", "", false, errors.New(usage)
	}
}

// parseGroupClearArgs parses `clear [<project-key>|--here]`, the same
// hand-rolled way parseGroupSetArgs does.
func parseGroupClearArgs(args []string) (projectKey string, here bool, err error) {
	const usage = "usage: backstory group clear [<project-key>|--here]"
	var positional []string
	for _, a := range args {
		switch {
		case a == "--here" || a == "-here":
			here = true
		case strings.HasPrefix(a, "-"):
			return "", false, fmt.Errorf("unknown flag %q; %s", a, usage)
		default:
			positional = append(positional, a)
		}
	}
	switch {
	case here && len(positional) == 0:
		return "", true, nil
	case !here && len(positional) == 1:
		return positional[0], false, nil
	default:
		return "", false, errors.New(usage)
	}
}
