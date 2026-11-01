import QtQuick
import Quickshell
import Quickshell.Io

// The one shared settle/click/launch helper every panel QML test goes
// through (task bf001e64; supersedes 175f1ce3's site-by-site patches).
//
// Why it exists: a raw mouseClick() + wait(20) + immediate assertion bakes
// in "the layout is final, the click is delivered, and the launch is
// recorded within 20 ms". Under CPU load none of that holds, and each fix
// that patched one site left the class intact. Here an ACTION waits for the
// target to be ready and confirms the click landed; an EFFECT is read only
// through settled()/launchedNext()/detachedNext(), which poll until the
// positive condition holds. panel_qml_actions_lint_test.go fails the build
// if a tests/*.qml file calls mouseClick(/mousePress( (or any other raw
// pointer call) outside this file, so the pattern cannot creep back.
//
// Usage: declare `TestActions { id: act; testCase: testCase }` in a TestCase.
QtObject {
  id: root

  // The TestCase whose mouse*/key*/tryVerify this helper drives.
  property var testCase: null

  // Every wait below ends at this bound, never earlier on a green run: a
  // condition that holds returns on the first poll. Sized for 2x CPU
  // oversubscription with headroom (the round-2 stress load).
  property int timeoutMs: 5000

  // A target's geometry must be unchanged across this many consecutive
  // polls before it counts as laid out (a Flickable/Column still reflowing
  // moves its children between polls).
  property int stableFrames: 2

  // ---- readiness ----

  function isEnabledChain(item) {
    for (var p = item; p; p = p.parent) {
      if (p.enabled === false) return false
    }
    return true
  }

  function isReady(item) {
    return item !== null && item !== undefined
      && item.visible === true
      && isEnabledChain(item)
      && item.width > 0 && item.height > 0
  }

  // ---- delivery detection ----

  function isMouseArea(n) {
    return typeof n.containsMouse === "boolean" && typeof n.acceptedButtons === "number"
  }

  function containsPoint(area, item, x, y) {
    if (typeof area.mapFromItem !== "function") return false
    var pt = area.mapFromItem(item, x, y)
    return pt.x >= 0 && pt.y >= 0 && pt.x <= area.width && pt.y <= area.height
  }

  // The things that report a click landing at (x, y) of item: item's own
  // `clicked` signal plus every MouseArea over that point in its subtree;
  // when that finds nothing (a bare Text whose MouseArea is a sibling), the
  // search widens one ancestor at a time, at most three levels.
  function clickSources(item, x, y) {
    var out = []
    function visit(n, depth) {
      if (!n || depth > 8) return
      if (isMouseArea(n) && containsPoint(n, item, x, y) && out.indexOf(n) < 0) out.push(n)
      var kids = n.children
      if (kids && kids.length !== undefined) {
        for (var i = 0; i < kids.length; i++) visit(kids[i], depth + 1)
      }
    }
    if (typeof item.clicked === "function" && !isMouseArea(item)) out.push(item)
    var scope = item
    for (var up = 0; up <= 3 && scope; up++) {
      visit(scope, 0)
      if (out.length > 0) break
      scope = scope.parent
    }
    return out
  }

  // ---- actions ----

  // Click a target only once it is visible, enabled all the way up, laid out
  // and holding still, then confirm the click was delivered (retrying while
  // the target swallows it, e.g. a button still arming). `target` is an item
  // or a function returning one (re-evaluated every poll, so a delegate that
  // gets rebuilt is re-found rather than clicked stale).
  //
  // opts: button (Qt.LeftButton default), x/y (default the centre), desc,
  //       until (predicate: the click's positive effect; waited on after delivery).
  function clickWhenReady(target, opts) {
    var o = opts || {}
    var desc = o.desc || "click target"
    var button = o.button === undefined ? Qt.LeftButton : o.button
    var item = null
    var lastPos = ""
    var stable = 0
    var sources = null
    var delivered = 0
    var handlers = []

    function resolve() { return typeof target === "function" ? target() : target }
    function onDelivered() { delivered++ }
    function detach() {
      for (var i = 0; i < handlers.length; i++) {
        try { handlers[i].sig.disconnect(onDelivered) } catch (e) { /* target destroyed by the click */ }
      }
      handlers = []
    }

    var done = false

    // tryVerify re-evaluates its predicate once more after it first returns
    // true, so a success must be sticky: never re-resolve or re-click then.
    testCase.tryVerify(function () {
      if (done) return true
      var next = resolve()
      if (next !== item) { detach(); item = next; lastPos = ""; stable = 0; sources = null; delivered = 0 }
      if (!isReady(item)) { stable = 0; return false }
      var at = item.mapToItem(null, 0, 0)
      var pos = at.x + "," + at.y + "," + item.width + "," + item.height
      stable = pos === lastPos ? stable + 1 : 0
      lastPos = pos
      if (stable < root.stableFrames) return false

      if (delivered > 0) { done = true; return true }
      var x = o.x === undefined ? item.width / 2 : o.x
      var y = o.y === undefined ? item.height / 2 : o.y
      if (sources === null) {
        sources = clickSources(item, x, y)
        for (var i = 0; i < sources.length; i++) {
          sources[i].clicked.connect(onDelivered)
          handlers.push({ sig: sources[i].clicked })
        }
      }
      testCase.mouseClick(item, x, y, button)
      // Nothing observable to confirm against: one click is all there is.
      done = sources.length === 0 || delivered > 0
      return done
    }, root.timeoutMs, "never became clickable (visible, enabled, laid out) or never took the click: " + desc)
    detach()

    if (o.until) settled(o.until, "no effect after clicking " + desc)
  }

  // For a control the mouse cannot reach (scrolled out of the viewport):
  // wait until it is ready, then fire its own `clicked` handler. The same
  // readiness gate as clickWhenReady, minus the pointer.
  function activateWhenReady(target, opts) {
    var o = opts || {}
    var item = null
    testCase.tryVerify(function () {
      item = typeof target === "function" ? target() : target
      return item !== null && item !== undefined && item.visible === true && isEnabledChain(item) && typeof item.clicked === "function"
    }, root.timeoutMs, "never became activatable (visible, enabled): " + (o.desc || "target"))
    item.clicked()
    if (o.until) settled(o.until, "no effect after activating " + (o.desc || "target"))
  }

  // Look a target up by predicate over the visible tree under `root`, then
  // click it via clickWhenReady (re-finding on every poll).
  function clickFound(rootItem, find, opts) {
    clickWhenReady(function () { return find(rootItem) }, opts)
  }

  // Press a key. Kept next to the pointer actions so a test has one place to
  // go; delivery is synchronous to the focused item, effects are read through
  // settled()/launchedNext().
  function pressKey(key, mods) {
    testCase.keyClick(key, mods === undefined ? Qt.NoModifier : mods)
  }

  // ---- effects ----

  // Moves the pointer over target (default its centre) and waits for
  // opts.until, the hover's effect, when given. Pair with moveAway(): a
  // cursor left over a row hovers it in the next test's panel. It arrives
  // in three steps inside target, as a real move does: Omarchy's
  // PointerMoveGate takes a row's first sample as where the pointer is, not
  // as a move, and Qt 6.4 reports the step that enters a row as an enter
  // alone, with no position change.
  function hover(target, opts) {
    var o = opts || {}
    var x = o.x === undefined ? target.width / 2 : o.x
    var y = o.y === undefined ? target.height / 2 : o.y
    var step = x >= 6 ? -3 : 3
    testCase.mouseMove(target, x + 2 * step, y)
    testCase.mouseMove(target, x + step, y)
    testCase.mouseMove(target, x, y)
    if (o.until) root.settled(o.until, (o.desc || "hover") + ": its effect never showed")
  }

  // Parks the pointer at item's top-left corner, off any row.
  function moveAway(item) {
    testCase.mouseMove(item, 0, 0)
  }

  // Wait for a UI condition to hold.
  function settled(pred, msg) {
    testCase.tryVerify(pred, root.timeoutMs, msg === undefined ? "condition never settled" : msg)
  }

  // Wait until snapshot() (any JSON-able view of the UI) has been identical
  // across stableFrames + 1 consecutive polls: the tree has stopped reflowing.
  // For the baseline render after a data load, before a test reads it.
  function quiescent(snapshot, msg) {
    var last = null
    var same = 0
    var done = false
    testCase.tryVerify(function () {
      if (done) return true
      var now = JSON.stringify(snapshot())
      same = now === last ? same + 1 : 0
      last = now
      done = same >= root.stableFrames
      return done
    }, root.timeoutMs, msg === undefined ? "UI never stopped changing" : msg)
  }

  // Wait for obj[prop] to equal value (QtTest's fuzzy compare for reals).
  function settledEq(obj, prop, value, msg) {
    testCase.tryCompare(obj, prop, value, root.timeoutMs, msg)
  }

  // Wait for `count` recorded Process launches satisfying pred, and return
  // them all (oldest first). pred(run) sees { command, workingDirectory }.
  function launchedCount(pred, count, msg) {
    function matching() { return ProcessController.runs.filter(pred) }
    testCase.tryVerify(function () { return matching().length >= count }, root.timeoutMs,
      (msg || "launch") + ": expected " + count + " matching Process run(s); ran "
        + ProcessController.runs.map(function (r) { return JSON.stringify(r.command) }).join(" | "))
    return matching()
  }

  // Wait for one Process launch satisfying pred and return it.
  function launchedNext(pred, msg) {
    return launchedCount(pred, 1, msg)[0]
  }

  // Same for Quickshell.execDetached() calls.
  function detachedNext(pred, msg) {
    function matching() { return Quickshell.execDetachedCalls.filter(pred) }
    testCase.tryVerify(function () { return matching().length >= 1 }, root.timeoutMs,
      (msg || "detached launch") + ": no matching execDetached call; saw "
        + Quickshell.execDetachedCalls.map(function (c) { return JSON.stringify(c.command) }).join(" | "))
    return matching()[0]
  }

  // Explicit settle point for a NEGATIVE assertion ("nothing launched", "count
  // stays 0"): the caller must already have settled() on a positive effect
  // that orders after the thing that must not have happened; this just lets
  // already-queued events drain before the synchronous read.
  function drain() {
    testCase.wait(20)
  }
}
