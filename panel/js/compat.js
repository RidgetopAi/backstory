.pragma library

// Omarchy 4.0 and 4.5 differ in what qs.Commons.Style offers. Each helper
// takes the Style singleton itself and tests for the member at the call, so
// a missing one is never read as a property (a TypeError on 4.0) and no
// version string is parsed: the host either has the helper or it does not.

// duration: Style.duration (Omarchy 4.5+) is `ms`, or 0 when `omarchy toggle
// animations` is off; 4.0 has no such helper, so the animation always runs.
function duration(style, ms) {
  return typeof style.duration === "function" ? style.duration(ms) : ms
}
