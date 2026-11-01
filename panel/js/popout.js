.pragma library

// The bar popup's pop-out into the window (BarWidget.qml -> Panel.qml). The
// popup sends where its card is on screen as `from`; the window starts there,
// then rests a bar's height further from the bar than the card, and a bar's
// height along it toward the middle of the monitor, one diagonal move. Any
// process that can summon the window can send `from`, so a rect that isn't
// plain numbers in range, on a known bar edge, is refused.

var LIMIT = 100000
var EDGES = ["top", "bottom", "left", "right"]

function isCoord(v) {
  return typeof v === "number" && isFinite(v) && Math.abs(v) <= LIMIT
}

// from: { x, y, w, h } of the popup's card (screen, logical px), the bar's
// thickness `bar` and the card's `gap` from it, the monitor's origin monX,
// monY and size monW, monH, and the bar's `edge`. Returns the same, rounded,
// or null when anything is off.
function fromRect(v) {
  if (!v || typeof v !== "object") return null
  var keys = ["x", "y", "w", "h", "bar", "gap", "monX", "monY", "monW", "monH"]
  for (var i = 0; i < keys.length; i++) if (!isCoord(v[keys[i]])) return null
  if (v.w <= 0 || v.h <= 0 || v.bar < 0 || v.gap < 0 || v.monW <= 0 || v.monH <= 0) return null
  if (EDGES.indexOf(v.edge) < 0) return null
  var r = { edge: v.edge }
  for (var k = 0; k < keys.length; k++) r[keys[k]] = Math.round(v[keys[k]])
  return r
}

// A bar's height toward the monitor's middle along an axis: +bar when the
// card's middle is before the monitor's, else -bar.
function towardMiddle(cardStart, cardSize, monStart, monSize, bar) {
  return cardStart + cardSize / 2 < monStart + monSize / 2 ? bar : -bar
}

// Where the window rests, taller by `grow` (the grab handle, on its top).
// Away from the bar: the card hangs one gap from it, the window rests a
// bar's height from it instead; over a bottom bar the growth goes upward.
function restRect(from, grow) {
  var g = Math.round(grow)
  var away = from.bar - from.gap
  var x = from.x, y = from.y
  if (from.edge === "top" || from.edge === "bottom") {
    x += towardMiddle(from.x, from.w, from.monX, from.monW, from.bar)
    y += from.edge === "top" ? away : -away - g
  } else {
    y += towardMiddle(from.y, from.h, from.monY, from.monH, from.bar)
    x += from.edge === "left" ? away : -away
  }
  return { x: x, y: y, w: from.w, h: from.h + g }
}
