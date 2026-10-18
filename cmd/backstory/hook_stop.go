package main

// runStopHook is `backstory hook stop`: a silent exit-0 no-op. The Stop
// handoff nudge it used to run is gone (it re-blocked at the end of every
// turn); `install claude` no longer registers it and removes the entry an
// older install wrote. The subcommand stays so a stale registration left in
// a settings.json keeps exiting 0 instead of erroring on every turn. It does
// not read stdin or touch the daemon.
func runStopHook() int { return 0 }
