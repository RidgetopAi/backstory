package mcp

import (
	"encoding/json"
	"regexp"
	"time"

	"github.com/RidgetopAi/backstory/internal/payload"
	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// CommitAskText is the one line the PostToolUse hooks hand back as
// additionalContext after a session's first commit. It is context, never a
// block decision, so it costs no extra model turn.
const CommitAskText = "Backstory: you committed. If this finished something, `note` an outcome (one or two lines; skip if trivial)."

// gitCommitCommand matches a shell command that runs `git commit` (any
// arguments, any leading git options such as -C dir or -c k=v). `git
// commit-tree` and friends do not match: the subcommand must end at
// whitespace or the end of the command.
var gitCommitCommand = regexp.MustCompile(`(?:^|[\s;&|(){}])git\s+(?:(?:-C|-c|--git-dir|--work-tree|--namespace)\s+\S+\s+|--?[A-Za-z][\w-]*(?:=\S+)?\s+)*commit(?:\s|$)`)

// isGitCommitCommand reports whether command runs `git commit`.
func isGitCommitCommand(command string) bool {
	return gitCommitCommand.MatchString(command)
}

// headReader is the cheap HEAD lookup a Git implementation may offer. The
// commit trigger runs on every Bash call, so it prefers it over State (which
// also lists and hashes the dirty tree); an implementation without it falls
// back to State.
type headReader interface {
	Head(cwd string) (string, bool)
}

// observeHead reads cwd's HEAD. ok is false for an unknown cwd, a non-git
// directory, a failed git command, or a repo with no commit yet.
func observeHead(git project.Git, cwd string) (string, bool) {
	if cwd == "" {
		return "", false
	}
	if hr, ok := git.(headReader); ok {
		head, ok := hr.Head(cwd)
		return head, ok && head != ""
	}
	state, ok := git.State(cwd)
	return state.Head, ok && state.Head != ""
}

// sessionLastHead is the HEAD the session last saw: its newest session.head
// event, else the session-start git observation. asked reports whether the
// commit ask was already settled for this session.
func sessionLastHead(st *store.Store, sessionID string) (head string, asked bool, err error) {
	events, err := st.EventsForSessionTimeline(sessionID, time.Time{}, payload.KindSessionHead, 0)
	if err != nil {
		return "", false, err
	}
	for _, e := range events {
		var sh payload.SessionHead
		if json.Unmarshal([]byte(e.Payload), &sh) != nil {
			continue
		}
		head = sh.Head
		asked = asked || sh.Asked
	}
	if head != "" {
		return head, asked, nil
	}
	gs, err := st.EventsForSessionTimeline(sessionID, time.Time{}, payload.KindSessionGitState, 0)
	if err != nil {
		return "", false, err
	}
	for _, e := range gs {
		var s payload.SessionGitState
		if json.Unmarshal([]byte(e.Payload), &s) == nil && s.Phase == payload.GitStatePhaseStart && !s.CouldNotObserve {
			return s.Head, asked, nil
		}
	}
	return "", asked, nil
}

// commitAsk keeps the session's last-seen HEAD current and decides whether
// this Bash call earns the one-per-session outcome ask. It returns the
// additionalContext line, or "" — and "" on every error, since the ask is
// never worth failing a tool call over.
//
// Only a caller that opted in (p.CommitAsk, set only by a hook that can show the line, and
// only on a Bash call) runs this at all: a caller that cannot show the ask
// must not move the session's last-seen HEAD either.
//
// The ask needs all of: the command runs `git commit`, the call did not fail, the
// session's cwd HEAD differs from the one it last saw (so a commit that
// moved nothing, or HEAD moved by another agent or a pull, never counts),
// no ask was settled earlier in this session, and the session has written
// no record. Whether or not it fires, the first commit settles the ask, so
// it never comes back.
func commitAsk(st *store.Store, git project.Git, sessionID string, p PostToolUseParams) string {
	cwd, err := st.SessionCWD(sessionID)
	if err != nil {
		return ""
	}
	head, ok := observeHead(git, cwd)
	if !ok {
		return ""
	}
	last, asked, err := sessionLastHead(st, sessionID)
	if err != nil || head == last {
		return ""
	}
	commit := !p.IsError && !p.Interrupted && (p.Exit == nil || *p.Exit == 0) &&
		isGitCommitCommand(p.Command) && last != ""
	settle := commit && !asked
	if err := appendSessionHead(st, sessionID, head, settle); err != nil {
		return ""
	}
	if !settle {
		return ""
	}
	wrote, err := st.SessionHasRecord(sessionID)
	if err != nil || wrote {
		return ""
	}
	return CommitAskText
}

func appendSessionHead(st *store.Store, sessionID, head string, asked bool) error {
	b, err := json.Marshal(payload.SessionHead{Head: head, Asked: asked})
	if err != nil {
		return err
	}
	_, err = st.AppendEvent(store.Event{
		TS: time.Now(), Kind: payload.KindSessionHead, SessionID: sessionID,
		Source: "daemon", Payload: string(b),
	})
	return err
}
