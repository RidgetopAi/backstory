# Panel contract — This Week

Decision `9be5c1d5`: This Week is one struct — Attention, Where you left off, The week —
built by `internal/week.Build` and rendered two ways: as text (`backstory this-week`) and
as JSON (`backstory this-week --json`). The JSON is the contract the Quickshell This Week
panel (`PLAN.md` §Phase 4 "Human look") reads. This document is that contract: every field
in it, its type, and when it is present.

Source of truth: `cmd/backstory/thisweek.go`'s `thisWeekOutputJSON` and its nested types.
If this document and that code ever disagree, the code wins and this document is out of
date — file a punch, do not trust this page over `go doc`.

## Window

Every section reads the same 7-day window: the UTC calendar day of `backstory
this-week`'s own run time, and the 6 UTC calendar days before it (`internal/week.WindowDays`,
`internal/week.windowStart`). There is no flag to change it — the window is part of This
Week's own definition, not a per-call option.

## Top level

```json
{
  "attention":     [ AttentionItem, ... ],
  "where_left_off": [ WhereLeftOffRow, ... ],
  "week":          [ DayProjectStats, ... ]
}
```

All three arrays are always present, always arrays (never `null`, never omitted) — an empty
`attention` array is itself the signal "nothing to flag this week" (decision `9be5c1d5`),
not the absence of a key a panel has to guard against with an existence check.

## `here` and `window` — `--here DIR|auto`

`backstory this-week --json --here <DIR|auto>` is optional. Without it the output is
byte-for-byte unchanged: no `here` key, no `window` fields.

`here` (top level, only with `--here`):

| Field          | Type   | Meaning |
|----------------|--------|---------|
| `project_key`  | string | `internal/project.Key` of the folder — the daemon's own function, so a worktree resolves to its main checkout's key and a non-git folder under a workspace dir to the workspace key. `""` only when `source` is `recent` and there is no activity at all. |
| `display_name` | string | The stored project's display name when known, else the folder's label. |
| `cwd`          | string | The resolved folder (for `recent`: the project's `cwd`). |
| `source`       | string | `arg` (DIR given), `focused` (the focused terminal's folder), `recent` (the most recently active where-you-left-off project — `--here auto` could not resolve a focused folder). |

`--here auto` runs `hyprctl activewindow -j`, walks the window pid's `/proc` descendants and
takes the folder of a `tmux: client` descendant (`tmux display-message -c <its tty> -p
'#{pane_current_path}'`), else the cwd of the deepest descendant (ties → newest). A missing
or failing `hyprctl`/`tmux`, or a focused window with no descendant (a browser), degrades to
`source: "recent"`: never a non-zero exit, stdout is always valid JSON.

With `--here auto` every `ProjectSummary` (standalone or group child) also carries `window`:
the Hyprland address of an open window (`hyprctl clients -j`) whose folder, resolved by the
same rule, maps to that project; `""` when none; with several, the lowest `focusHistoryID`.
The rule looks at every pane of every window of each attached tmux client's session
(`tmux list-panes -s`), not only the active pane, so a project open in a background tmux window
still gets its terminal's address.

`tmux` (string, present exactly when `window` is, i.e. only with `--here auto`): the tmux
target `session:window.pane` (numeric window and pane indexes, e.g. `Work:1.0`) of the pane
holding the project inside that `window`; with several panes of one project in the window, the
active pane of the active window, else the first. `""` when there is no `window`, the window
has no tmux client, or the session name is not a safe argv element (`[A-Za-z0-9_-]`). A panel
validates it before use and, with a valid one, focuses the window then runs
`tmux select-window -t <session:window>` and `tmux select-pane -t <target>`.

## Attention — `AttentionItem`

Positive evidence only, never elapsed time and never the absence of activity
(`AGENT-CONTRACT.md` §Outcomes are three-state). Scoped to projects with activity in the
window (the same set Where-you-left-off lists).

| Field          | Type       | Always present | Meaning |
|----------------|------------|-----------------|---------|
| `kind`         | string enum | yes | One of the four kinds below. |
| `project_key`  | string     | yes | The project the item is about. |
| `reason`       | string     | yes | One human-readable line. |
| `evidence_ids` | string[]   | yes (never `null`, may be empty) | Record ids and/or timeline event ids (event ids as decimal strings) that are this item's positive evidence. |

`kind` is one of:

- `possibly-stale-handoff` — the project's latest handoff has positive evidence against it
  (a contradiction, a later same-project record, or a later timeline event touching a path
  it names — `store.HandoffFreshness`, decision `bcc9fa54`). `evidence_ids` are the record
  and/or event ids `store.HandoffFreshness` cited.
- `uncommitted-at-session-end` — a `session.git_state` event this week observed the session's
  repo with `uncommitted_count > 0` at session end. A `could-not-observe` git-state event
  (the repo could not be read, or was not a git working tree) NEVER produces this item —
  `could-not-observe` is its own value, never folded into a count (`SCHEMA.md` invariant 7).
  `evidence_ids` is the one `session.git_state` event id.
- `contradiction` — a `contradicts` edge into a record in this project, where the
  contradicting record was itself inserted this week. `evidence_ids` is the contradicting
  record's own evidence (the timeline event ids it cited — `AGENT-CONTRACT.md` §Outcomes:
  "contradiction only on positive evidence").
- `expired-claim` — a non-tombstoned `claim` record whose `expires_at` has passed, with no
  `produced_outcome` edge touching it (nothing has recorded an outcome for it).
  `evidence_ids` is the claim's own record id.

## Where you left off — `WhereLeftOffRow`

One row per project with activity in the window, most recently active first. A project in a
user group (`store.SetProjectGroup`, human-only) collapses into one row with its
group-mates as children, rather than appearing as its own row (decision `9be5c1d5`). A
workspace identity (`project.IsWorkspaceKey`) never appears here, standalone or as a child
(decision `bcc9fa54`: a workspace is not a project).

```json
{ "project": ProjectSummary }                          // standalone row
{ "group": "widget-suite", "children": [ProjectSummary, ...] }  // group row
```

`group` and `children` are present together, on a group row, and absent together on a
standalone row; `project` is present only on a standalone row. A panel branches on whether
`group` is set.

### `ProjectSummary`

| Field                | Type    | Always present | Meaning |
|-----------------------|---------|-----------------|---------|
| `project_key`         | string  | yes | `internal/project.Key`'s output. |
| `display_name`        | string  | yes | The project's stored toplevel path, made workspace-relative (`projects/omarcade`) when it lives inside a configured workspace dir, else its basename (falls back to `project_key` if the project has no `projects` row, which should not happen for anything this endpoint lists). |
| `cwd`                 | string  | yes | The most recently started session's cwd — where a click-to-reopen action should open a terminal. |
| `last_activity`       | string (RFC3339Nano, UTC) | yes | The latest of the project's own session starts, timeline events, and record inserts. |
| `handoff_id`          | string  | only if the project has a handoff record | The latest handoff's record id. |
| `handoff_first_line`  | string  | only if the project has a handoff record | The first line of that handoff's text. |
| `handoff_stale`       | bool    | only `true` (omitted, meaning `false`, otherwise) | Mirrors whether a `possibly-stale-handoff` Attention item exists for this project — the same `store.HandoffFreshness` call feeds both, so they can never disagree. |
| `handoff_next`        | string  | yes (`""` when there is no handoff) | The handoff's stored `next` when set; otherwise its first line with leading boilerplate stripped (a leading ★, the word HANDOFF, a leading ISO date with optional time/Z, and the separators `-` `—` `.` `:` between them). |
| `last_agent`          | string  | yes (`""` when unknown) | The agent of the project's most recently started session. |

## The week — `DayProjectStats`

One entry per (day, project) pair that had ANY activity that day — sessions, files touched,
or records written; a project with nothing to show on a given day gets no entry for it
(there is no zero-filled placeholder row). Sorted most recent day first, then by
`project_key`.

| Field             | Type   | Meaning |
|-------------------|--------|---------|
| `day`             | string (`YYYY-MM-DD`, UTC calendar day) | Which day in the window. |
| `project_key`     | string | The project. |
| `display_name`    | string | Same rule as `ProjectSummary.display_name`. |
| `sessions`        | int    | Distinct sessions with at least one timeline event that day (including a session that merely started with no tool use — its own `session.start` event still counts). |
| `files_touched`   | int    | Distinct file paths a mutating tool (`payload.IsMutatingFileTool`: Edit, Write, MultiEdit, NotebookEdit — never Read) touched that day. |
| `records_written` | int    | Records (any kind) inserted that day. |
