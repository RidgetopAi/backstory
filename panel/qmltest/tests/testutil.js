.pragma library

// Shared QML-test helpers (task 4fe02e30 Part 1 harness). Kept separate
// from the plugin's OWN js/ (js/model.js etc, panel_contract_test.go's own
// subject) — this file is test-only infrastructure, never loaded by the
// real plugin.

// collectVisibleTexts walks item's visible descendants (skipping a
// subtree entirely once an ancestor's own `visible` is false — Qt Quick
// positioners already do the same for layout, PANEL-CONTRACT.md's own
// "rendered only when non-empty" sections rely on exactly this) and
// returns every Text.text found, so a test can assert a golden's display
// names actually reached the screen (DONE WHEN clause 2).
function collectVisibleTexts(item) {
  var out = []
  walkVisible(item, function (node) {
    if (node.text !== undefined && typeof node.text === "string") {
      out.push(node.text)
    }
  })
  return out
}

// collectOutOfBounds walks root's visible descendants and returns a list
// of { path, rect } for every item whose rect, mapped into reference's
// coordinate space, is not fully contained within [0, 0, boundsWidth,
// boundsHeight] (DONE WHEN clause 3's generic layout walker). `epsilon`
// absorbs sub-pixel rounding from mapToItem.
function collectOutOfBounds(root, reference, boundsWidth, boundsHeight, epsilon) {
  var eps = epsilon === undefined ? 0.5 : epsilon
  var bad = []
  walkVisible(root, function (node, path) {
    if (node === reference) return
    if (typeof node.width !== "number" || typeof node.height !== "number") return
    if (node.width <= 0 && node.height <= 0) return
    if (typeof node.mapToItem !== "function") return

    var topLeft = node.mapToItem(reference, 0, 0)
    var rect = { x: topLeft.x, y: topLeft.y, width: node.width, height: node.height }

    var withinX = rect.x >= -eps && (rect.x + rect.width) <= (boundsWidth + eps)
    var withinY = rect.y >= -eps && (rect.y + rect.height) <= (boundsHeight + eps)
    if (!withinX || !withinY) {
      bad.push({ path: path, rect: rect })
    }
  })
  return bad
}

// walkVisible calls fn(node, path) for `root` and every descendant reached
// through a chain of `visible !== false` ancestors, using whichever of
// `children`/`data`/`contentItem` the node actually exposes (PanelWindow's
// own stub exposes `contentItem`, not `children` — see
// qmltest/stubs/Quickshell/PanelWindow.qml).
function walkVisible(root, fn, path) {
  if (!root) return
  if (root.visible === false) return
  var here = path || "root"
  fn(root, here)

  var kids = childrenOf(root)
  for (var i = 0; i < kids.length; i++) {
    walkVisible(kids[i], fn, here + ">" + (kids[i].objectName || kids[i].toString()))
  }
}

// findFirst returns the first visible descendant of root (root included)
// for which predicate(node) is true, walking the same visible-only chain
// as collectVisibleTexts/collectOutOfBounds above.
function findFirst(root, predicate) {
  var found = null
  walkVisible(root, function (node) {
    if (found === null && predicate(node)) found = node
  })
  return found
}

function childrenOf(node) {
  if (node.contentItem) return [node.contentItem]
  if (node.children && node.children.length !== undefined) {
    var out = []
    for (var i = 0; i < node.children.length; i++) out.push(node.children[i])
    return out
  }
  return []
}
