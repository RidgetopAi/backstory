package recall

import (
	"fmt"

	"github.com/RidgetopAi/backstory/internal/project"
	"github.com/RidgetopAi/backstory/internal/store"
)

// maxGitLookupsPerRecall bounds how many distinct (project, sha) pairs one
// Build call resolves against git (task 5615ddae: "recall over many records
// must not run one git process per record unboundedly"). Records sharing a
// project and stamped sha share one lookup; a pair past the cap is reported
// with its sha and a reason, never a count.
const maxGitLookupsPerRecall = 32

// shortSHALen is how many leading hex digits of a stamped sha recall shows.
const shortSHALen = 7

// GitStamp is a record's write-time repo HEAD compared with the repo's HEAD
// at read time. CommitsSince is nil — with Reason set — when the count could
// not be computed.
type GitStamp struct {
	SHA          string
	Short        string
	CommitsSince *int
	Reason       string
}

type gitLookup struct {
	commits *int
	reason  string
}

// gitStamper resolves stamps for one Build call, caching per project (its
// toplevel) and per (project, sha).
type gitStamper struct {
	st        *store.Store
	toplevels map[string]string
	lookups   map[[2]string]gitLookup
	budget    int
}

func newGitStamper(st *store.Store) *gitStamper {
	return &gitStamper{
		st:        st,
		toplevels: map[string]string{},
		lookups:   map[[2]string]gitLookup{},
		budget:    maxGitLookupsPerRecall,
	}
}

func (g *gitStamper) toplevel(projectKey string) (string, error) {
	if top, ok := g.toplevels[projectKey]; ok {
		return top, nil
	}
	p, found, err := g.st.GetProject(projectKey)
	if err != nil {
		return "", fmt.Errorf("recall: git stamp project %s: %w", projectKey, err)
	}
	top := ""
	if found {
		top = p.Toplevel
	}
	g.toplevels[projectKey] = top
	return top, nil
}

// stamp returns rec's GitStamp, or nil when rec carries no git_head.
func (g *gitStamper) stamp(rec store.Record) (*GitStamp, error) {
	if rec.GitHead == "" {
		return nil, nil
	}
	s := &GitStamp{SHA: rec.GitHead, Short: shortSHA(rec.GitHead)}
	top, err := g.toplevel(rec.ProjectKey)
	if err != nil {
		return nil, err
	}
	key := [2]string{top, rec.GitHead}
	lk, ok := g.lookups[key]
	if !ok {
		if g.budget <= 0 {
			lk = gitLookup{reason: "git lookup limit reached for this recall"}
		} else {
			g.budget--
			n, reason, ok := project.CommitsSince(top, rec.GitHead)
			if ok {
				lk = gitLookup{commits: &n}
			} else {
				lk = gitLookup{reason: reason}
			}
		}
		g.lookups[key] = lk
	}
	s.CommitsSince, s.Reason = lk.commits, lk.reason
	return s, nil
}

func shortSHA(sha string) string {
	if len(sha) <= shortSHALen {
		return sha
	}
	return sha[:shortSHALen]
}

// gitLabel renders a stamp for the item header: "written at abc1234; repo
// now 2 commits later", "written at abc1234; at current HEAD", or, when no
// count exists, "written at abc1234; repo drift unknown (<reason>)".
func gitLabel(s *GitStamp) string {
	switch {
	case s.CommitsSince == nil:
		return fmt.Sprintf("written at %s; repo drift unknown (%s)", s.Short, s.Reason)
	case *s.CommitsSince == 0:
		return fmt.Sprintf("written at %s; at current HEAD", s.Short)
	case *s.CommitsSince == 1:
		return fmt.Sprintf("written at %s; repo now 1 commit later", s.Short)
	default:
		return fmt.Sprintf("written at %s; repo now %d commits later", s.Short, *s.CommitsSince)
	}
}

// GitLabel is the engine's own rendering of a stamp, for the MCP wire.
func GitLabel(s *GitStamp) string { return gitLabel(s) }
