package store

import (
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// homeFixture starts one session per (name, cwd, project key) row and returns
// the session id for each name.
func homeFixture(t *testing.T, s *Store, rows [][3]string) map[string]string {
	t.Helper()
	ids := map[string]string{}
	for _, r := range rows {
		mustUpsertProject(t, s, r[2])
		id, err := s.StartSession(StartSessionParams{
			Agent: "claude", CWD: r[1], ProjectKey: r[2], StartedAt: time.Now(), Origin: OriginLive,
		})
		if err != nil {
			t.Fatalf("StartSession(%s): %v", r[0], err)
		}
		ids[r[0]] = id
	}
	return ids
}

// namesOf maps sessionsForHome's sessions back to fixture names, sorted.
func namesOf(t *testing.T, ids map[string]string, got []Session) []string {
	t.Helper()
	byID := map[string]string{}
	for name, id := range ids {
		byID[id] = name
	}
	var out []string
	for _, sess := range got {
		out = append(out, byID[sess.ID])
	}
	sort.Strings(out)
	return out
}

func sameNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSessionsForHomeMembership pins which sessions belong to a home
// (sessionHomeKey): the enclosing workspace when a session's folder is inside
// one, at any depth and however the path is spelled, else its own
// project_key. sessionsForHome may narrow what it reads from the database,
// but never this answer.
func TestSessionsForHomeMembership(t *testing.T) {
	s := mustOpen(t, filepath.Join(t.TempDir(), "backstory.db"))
	ws := "/srv/ws"
	workspaces := []string{ws}
	wsKey := "workspace:" + ws
	repo := "/srv/repo/.git"

	ids := homeFixture(t, s, [][3]string{
		{"repo-own", "/srv/repo", repo},
		{"repo-key-but-in-ws", ws + "/repo-copy", repo},
		{"other-project", "/srv/other", "/srv/other/.git"},
		{"ws-root", ws, wsKey},
		{"ws-deep", ws + "/a/b/c", "/srv/ws/a/.git"},
		{"ws-trailing-slash", ws + "/", wsKey},
		{"ws-dot", ws + "/./d", "/srv/ws/d/.git"},
		{"ws-dotdot", "/srv/elsewhere/../ws/e", "/srv/ws/e/.git"},
		{"ws-double-slash", "/srv//ws/f", "/srv/ws/f/.git"},
		{"ws-key-outside", "/tmp/moved", wsKey},
		{"ws-lookalike", "/srv/ws-other/g", "/srv/ws-other/g/.git"},
	})

	cases := []struct {
		home string
		want []string
	}{
		{repo, []string{"repo-own"}},
		{wsKey, []string{"repo-key-but-in-ws", "ws-deep", "ws-dot", "ws-dotdot", "ws-double-slash", "ws-key-outside", "ws-root", "ws-trailing-slash"}},
	}
	for _, c := range cases {
		got, err := s.sessionsForHome(c.home, workspaces)
		if err != nil {
			t.Fatalf("sessionsForHome(%s): %v", c.home, err)
		}
		if names := namesOf(t, ids, got); !sameNames(names, c.want) {
			t.Errorf("sessionsForHome(%s) = %v; want %v", c.home, names, c.want)
		}
	}
}
