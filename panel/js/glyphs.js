.pragma library

// Nerd Font (Font Awesome PUA) glyphs used by this plugin's UI. Named here,
// once, rather than as unicode escapes scattered through the QML — a glyph
// swap is a one-line change, not a grep-and-replace.

function gear() { return "" }         // nf-fa-cog
function close() { return "" }        // nf-fa-times
function terminal() { return "" }     // nf-fa-terminal
function attention() { return "" }    // nf-fa-exclamation_triangle
function chevronRight() { return "" } // nf-fa-chevron_right
function chevronDown() { return "" }  // nf-fa-chevron_down
function resume() { return "" }       // nf-fa-refresh
function addProject() { return "" }   // nf-fa-plus
function week() { return "" }        // nf-fa-calendar
function back() { return "" } // nf-fa-arrow_left
function memory() { return "\uf02d" }       // nf-fa-book
function edit() { return "\uf044" }         // nf-fa-pencil_square_o
function trash() { return "\uf1f8" }        // nf-fa-trash
function history() { return "\uf1da" }      // nf-fa-history

// One glyph per record kind the store defines (SCHEMA.md records.kind CHECK).
var RECORD_KIND_GLYPHS = {
  decision: "\uf0e3", // nf-fa-gavel
  outcome: "\uf11e",  // nf-fa-flag_checkered
  handoff: "\uf064",  // nf-fa-share
  note: "\uf249",     // nf-fa-sticky_note
  claim: "\uf00c",    // nf-fa-check
  punch: "\uf0e7",    // nf-fa-bolt
  stage: "\uf0c9",    // nf-fa-bars
  confirm: "\uf058"   // nf-fa-check_circle
}

var RECORD_KIND_FALLBACK = "\uf111" // nf-fa-circle

// kind glyph for a record's kind; a kind the store does not define gets a
// neutral dot.
function recordKind(kind) {
  if (Object.prototype.hasOwnProperty.call(RECORD_KIND_GLYPHS, kind)) return RECORD_KIND_GLYPHS[kind]
  return RECORD_KIND_FALLBACK
}
