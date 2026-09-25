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

// projectKeyIN renders key's alias set (projectKeyAliases) as the
// "(?, ?)"-shaped SQL fragment a query's `project_key = ?` becomes
// `project_key IN <placeholders>`, plus the args to bind there, in order.
func projectKeyIN(key string) (placeholders string, args []any) {
	keys := projectKeyAliases(key)
	args = make([]any, len(keys))
	placeholders = "("
	for i, k := range keys {
		if i > 0 {
			placeholders += ", "
		}
		placeholders += "?"
		args[i] = k
	}
	placeholders += ")"
	return placeholders, args
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
