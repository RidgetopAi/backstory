.pragma library

// records.js is the ONLY place in this plugin that reads a field out of
// `backstory records --json [--history]` (decision 2d46dc99), the Memory
// view's data source. Same discipline as js/model.js: every other file goes
// through these accessors, so records_contract_test.go can find every field
// this plugin depends on by reading this one file and check each against
// the committed records goldens. Kept separate from model.js so the
// this-week contract stays untouched. Presentation helpers (labels, ages,
// time bounds) live in js/memory.js, which reads no JSON fields.

function topRecords(data) { return data.records || [] }
function projectKey(data) { return data.project_key }

function recordId(r) { return r.id }
function recordTs(r) { return r.ts }
function recordKind(r) { return r.kind }
function recordTier(r) { return r.tier }
function recordStatus(r) { return r.status }
function recordText(r) { return r.text || "" }
