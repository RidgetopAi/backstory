package project

// CachedGit wraps a Git and asks it about each folder's repository (Repo) at
// most once, for a short-lived command such as `this-week`, which resolves
// the same folders many times. RealGit's Repo runs four git commands per
// call. State is never cached: a folder's uncommitted count can change at
// any moment. Not for concurrent use, and not for long-lived processes such
// as the daemon, where a folder can become a repository later.
type CachedGit struct {
	inner Git
	repos map[string]cachedRepo
}

type cachedRepo struct {
	repo Repo
	ok   bool
}

// NewCachedGit returns a CachedGit over inner.
func NewCachedGit(inner Git) *CachedGit {
	return &CachedGit{inner: inner, repos: map[string]cachedRepo{}}
}

// Repo implements Git, remembering inner's answer for cwd.
func (g *CachedGit) Repo(cwd string) (Repo, bool) {
	if c, found := g.repos[cwd]; found {
		return c.repo, c.ok
	}
	r, ok := g.inner.Repo(cwd)
	g.repos[cwd] = cachedRepo{repo: r, ok: ok}
	return r, ok
}

// State implements Git by asking inner every time.
func (g *CachedGit) State(cwd string) (State, bool) {
	return g.inner.State(cwd)
}
