`description:` claims the intents — "what was I doing", "resume", "why did we", "backstory", "history", "last session".
if a SessionStart block is present, do not re-fetch; else call `recall` for this project once; if the Resume line carries a possibly-stale marker, verify it before acting and `confirm affirm` it if still true.
before changing a file whose recall shows a declared decision, read it.
when you choose between alternatives, `note decision` in one line — no ceremony.
claim "done" only with `note outcome` pointing at a `timeline` event id; a claim without evidence is recorded as a claim.
end with `note handoff`: what is true now, what is next, what not to do. Put the single next step in the handoff's optional `next` (one line, at most 200 characters; handoff only) so the panel can show it. Set `supersedes` to the Resume slot's id when it showed one; whenever any record replaces or corrects an earlier one, set `supersedes` to its id (or `confirm supersede`).
inferred records are hints; declared records are claims; the timeline is fact.
never put a secret in a note.
