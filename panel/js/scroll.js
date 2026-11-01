.pragma library

// Scrolling a Flickable so nobody has to scroll to find what a key or a
// click just produced.

function clampedY(flick, y) {
  return Math.max(0, Math.min(y, flick.contentHeight - flick.height))
}

function scrollBy(flick, dy) {
  flick.contentY = clampedY(flick, flick.contentY + dy)
}

// Scrolls flick so `target` lies inside its visible viewport; an item taller
// than the viewport is shown from its top. Every positioner between target
// and the content is laid out first, innermost first: a row's agent
// sub-rows sit in a nested Column that is not yet positioned right after the
// row expands.
function reveal(flick, target) {
  for (var p = target.parent; p && p !== flick.contentItem; p = p.parent)
    if (typeof p.forceLayout === "function") p.forceLayout()
  var top = target.mapToItem(flick.contentItem, 0, 0).y
  var bottom = top + target.height
  var y = flick.contentY
  if (bottom > y + flick.height) y = bottom - flick.height
  if (top < y) y = top
  flick.contentY = clampedY(flick, y)
}
