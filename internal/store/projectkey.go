package store

import "github.com/RidgetopAi/backstory/internal/project"

// projectKeyAliases resolves the read-side key set a project-scoped read
// must match for key (decision 1e53165a, task 50249f56): key itself, plus,
// when key is a workspace identity, the plain legacy key a pre-workspace
// build wrote under before the re-key (79f7b20e) minted the workspace:
// prefix — the same folder, the same identity, never carried across by any
// migration (records stay append-only: records_no_update forbids ever
// changing project_key, so a legacy row is matched at read time, not
// rewritten). A git repo key is never a workspace key
// (project.WorkspaceLegacyKey's own ok==false for one), so it is only ever
// aliased to itself — every call site in this package funnels through here,
// the one place the alias set is computed.
func projectKeyAliases(key string) []string {
	if legacy, ok := project.WorkspaceLegacyKey(key); ok {
		return []string{key, legacy}
	}
	return []string{key}
}

// projectKeyIN returns the two bind args a query's static, literal
// `project_key IN (?, ?)` clause needs for key's read-side alias set
// (projectKeyAliases): key and its legacy form when key has one, else key
// twice — an IN clause tolerates the duplicate; it is still exactly one
// distinct value, and every call site keeps its SQL text a fixed literal
// rather than one built by string concatenation (gosec G202) from a
// caller-varying placeholder count.
func projectKeyIN(key string) (a, b any) {
	keys := projectKeyAliases(key)
	if len(keys) == 2 {
		return keys[0], keys[1]
	}
	return keys[0], keys[0]
}

// projectKeyMatches reports whether candidate is key itself or one of its
// read-side aliases (projectKeyAliases) — the Go-side equivalent of
// `project_key IN <projectKeyIN(key)>` for a value already loaded into
// memory (confirm.go's supersede check), so it agrees with the alias set
// every query-based read uses rather than re-deriving its own notion of
// "the same project".
func projectKeyMatches(candidate, key string) bool {
	for _, k := range projectKeyAliases(key) {
		if candidate == k {
			return true
		}
	}
	return false
}
