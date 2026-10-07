---
name: backstory
description: "Backstory is the project's memory across sessions. Use when the user asks 'what was I doing', 'resume', 'why did we', 'backstory', 'history', or 'last session', and to record decisions, outcomes and handoffs with the backstory recall/note/timeline/confirm tools."
---
if a SessionStart block is present, do not re-fetch; else call `recall` for this project once; if the Resume line carries a possibly-stale marker, verify it before acting and `confirm affirm` it if still true.
before changing a file whose recall shows a declared decision, read it.
when you choose between alternatives, `note decision` in one line — no ceremony.
claim "done" only with `note outcome` pointing at a `timeline` event id; a claim without evidence is recorded as a claim.
end with `note handoff`: what is true now, what is next, what still must not be done. Do not carry forward instructions that applied only to the current session ("don't start X now" when X is the next step); `next` is an action the next session can start, with a precondition only if it is real and unmet. Put the single next step in the handoff's optional `next` (one line, at most 200 characters; handoff only) so the panel can show it. Set `supersedes` to the id of the record this replaces or corrects (the Resume slot's, or one in a decision note's `related_decisions`), or `confirm supersede`.
inferred records are hints; declared records are claims; the timeline is fact.
never put a secret in a note.
