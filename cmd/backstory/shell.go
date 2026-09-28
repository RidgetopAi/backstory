package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/RidgetopAi/backstory/internal/mcp"
)

// runShell is the `backstory shell` subcommand: init prints an integration
// snippet for a shell to eval from its own startup file; emit records one
// observed shell command with the daemon (task 7fe84ffb).
func runShell(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "backstory shell: usage: backstory shell init|emit")
		return 2
	}
	switch args[0] {
	case "init":
		return runShellInit(args[1:], stdout, stderr)
	case "emit":
		return runShellEmit(args[1:], stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "backstory shell: unknown subcommand %q\n\nusage: backstory shell init|emit\n", args[0])
		return 2
	}
}

// runShellInit prints the named shell's integration snippet to stdout. Only
// "bash" is supported today (part 1 of the shell capture punch); an
// unrecognized shell name is a usage error, not a silently empty snippet.
func runShellInit(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "backstory shell init: usage: backstory shell init bash")
		return 2
	}
	switch args[0] {
	case "bash":
		// io.WriteString, not fmt.Fprint: bashInitSnippet's own printf
		// format strings (e.g. '%s\n%s\n%s\n') make `go vet`'s printf
		// checker flag Fprint here as a possible missing Fprintf — it isn't;
		// this snippet is printed verbatim, never formatted.
		_, _ = io.WriteString(stdout, bashInitSnippet)
		return 0
	default:
		_, _ = fmt.Fprintf(stderr, "backstory shell init: unsupported shell %q (only \"bash\" is supported)\n", args[0])
		return 2
	}
}

// shellEmitParams mirrors mcp.ShellEmitParams: the flag values `backstory
// shell emit` was actually invoked with. It is never asked to carry a
// session, harness, or project — the daemon resolves the event's identity
// from the connection's own SO_PEERCRED + /proc ancestry alone (DONE WHEN
// clause 5), so there is no flag here that could even attempt to override
// it.
func runShellEmit(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("shell emit", flag.ContinueOnError)
	cmd := fs.String("cmd", "", "the command text observed (required)")
	cwd := fs.String("cwd", "", "the shell's cwd when the command ran")
	exit := fs.Int("exit", 0, "the command's exit status")
	durationMS := fs.Int("duration-ms", 0, "the command's wall-clock duration in milliseconds")
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *cmd == "" {
		_, _ = fmt.Fprintln(stderr, "backstory shell emit: --cmd is required")
		return 2
	}

	params := mcp.ShellEmitParams{Cmd: *cmd, CWD: *cwd, Exit: *exit, DurationMS: *durationMS}
	// callDaemon (cmd/backstory/hook.go) is the same dial-one-line-close
	// helper `backstory hook` uses: a harness declaring a "session" join key
	// is meaningless for a shell command (bash has no session_id concept),
	// so it is passed empty here.
	if _, err := callDaemon("", mcp.DaemonMethodShellEmit, params); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory shell emit:", err)
		return 0
	}
	return 0
}

// bashInitSnippet is `backstory shell init bash`'s printed output, evaled
// from ~/.bashrc (part 2 of task 7fe84ffb wires the eval line itself; this
// punch only prints the snippet). Design notes, each backed by an empirical
// check against a real bash -i (no controlling tty, matching how this
// punch's own tests and a real Omarchy login shell both run it):
//
//   - Guarded by _BACKSTORY_SHELL_READY so evaling it twice (a doubled eval
//     line, or a re-sourced rc file) registers every hook exactly once.
//     Guarded by `$- == *i*` so a non-interactive shell (a script that
//     happens to source .bashrc) never installs anything.
//   - PS0 and PROMPT_COMMAND are APPENDED to, never assigned outright: a
//     pre-existing PS0 stays a plain string (bash has no array form for it);
//     PROMPT_COMMAND is detected as an array via `declare -p` (bash 5.1+'s
//     array form) or a string, and appended to in whichever shape it already
//     has. Either way whatever the user already set keeps running.
//   - Command text and history sequence number are captured from `history
//     1` alone, parsed with a regex, NOT `fc -l -n -1`: measured against a
//     real bash -i, `fc -l -n -1` lags one command behind whenever it's
//     invoked through a function call (`history 1` does not — verified with
//     both behind the same two-level command-substitution nesting this
//     snippet actually uses). HISTTIMEFORMAT is cleared for the `history 1`
//     call alone (inside its own disposable subshell, so nothing needs
//     saving/restoring) so a user's own timestamp format never lands inside
//     the captured command text.
//   - The capture itself (PS0, i.e. _backstory_preexec) writes into a
//     per-shell state file rather than shell variables: PS0's `$(...)` is a
//     command substitution, which always forks a subshell in bash, so a
//     plain variable assignment inside it would vanish the instant the
//     subshell exits. Only a real filesystem write survives back to the
//     PROMPT_COMMAND (precmd) that later reads it — created once, mode 0600
//     via a temporary umask, at a path keyed on this shell's own $$ and
//     $RANDOM so two concurrent shells never collide. It is deliberately
//     left in $TMPDIR at shell exit rather than chased with an EXIT trap:
//     doing that safely would mean capturing and re-chaining whatever EXIT
//     trap the user already has, the same class of "never clobber what's
//     already there" problem PS0/PROMPT_COMMAND themselves solve, and it is
//     out of scope for this punch (class members deferred: EXIT-trap
//     cleanup of the per-shell state file — because the OS's normal /tmp
//     lifecycle already reclaims it, and correctly preserving a user's own
//     EXIT trap is its own separate, untested concern here).
//   - HISTCONTROL gets `:ignorespace` appended (once, only if neither
//     ignorespace nor ignoreboth is already present) so a command starting
//     with a space is never added to bash's own history at all — precmd's
//     "history sequence number unchanged since last time" check then skips
//     it by construction, with no separate leading-space check needed.
//     _backstory_last_histnum is seeded with the CURRENT history number at
//     install time (the snippet's own eval line, already in history by the
//     time it runs this) rather than left empty: a space-led command run as
//     the very first command afterward leaves history's last entry
//     unchanged at that same seeded number, which must compare equal (skip),
//     not "" != anything (looks new). precmd's own "nothing observed yet"
//     branch (an empty state file — the eval line's own first prompt, fired
//     before preexec has ever run) returns WITHOUT touching
//     _backstory_last_histnum at all, for the same reason: this snippet's
//     own tests caught an earlier version that cleared it to "" there,
//     wiping out the seeded baseline and making the very next (space-led)
//     command's unchanged history number look new again, emitting the eval
//     line's own stale command text under the space-led command's exit
//     status.
//   - Exit status is read as literally the first statement of precmd
//     (`local __bs_exit=$?`), before anything else in this function can
//     disturb it, and is always the function's own return value too — the
//     one place in this snippet where getting the ORDER of statements right
//     is the entire correctness argument.
//   - The actual `backstory shell emit` call is both backgrounded AND
//     wrapped in its own `( … & )` subshell rather than `cmd & disown`: a
//     job backgrounded inside a subshell is never added to the interactive
//     shell's own job table at all (nothing to disown), and the subshell
//     itself returns as soon as the fork completes — measured at ~10ms
//     against a `backstory` standing in on $PATH that sleeps 5s, i.e. the
//     "no daemon, or a hung one" case DONE WHEN clause 3 requires, with no
//     dependency on job control being enabled (a bash -i with no controlling
//     terminal — exactly how this punch's own tests, and `backstory shell
//     emit`'s background invocation itself, both run bash — prints "no job
//     control in this shell" and disown would not reliably apply there).
const bashInitSnippet = `if [[ $- == *i* && -z "${_BACKSTORY_SHELL_READY:-}" ]]; then
_BACKSTORY_SHELL_READY=1
_backstory_state_file="${TMPDIR:-/tmp}/.backstory-shell-$$-$RANDOM"
_backstory_last_histnum=""
if [[ "$(HISTTIMEFORMAT= history 1)" =~ ^[[:space:]]*([0-9]+) ]]; then
    _backstory_last_histnum="${BASH_REMATCH[1]}"
fi
(
    umask 077
    : > "$_backstory_state_file"
) 2>/dev/null

case ":${HISTCONTROL:-}:" in
    *:ignorespace:*|*:ignoreboth:*) ;;
    *)
        if [[ -z "${HISTCONTROL:-}" ]]; then
            HISTCONTROL=ignorespace
        else
            HISTCONTROL="${HISTCONTROL}:ignorespace"
        fi
        ;;
esac

_backstory_preexec() {
    local __bs_raw __bs_histnum="" __bs_cmd=""
    __bs_raw="$(HISTTIMEFORMAT= history 1)"
    if [[ "$__bs_raw" =~ ^[[:space:]]*([0-9]+)[[:space:]]+(.*)$ ]]; then
        __bs_histnum="${BASH_REMATCH[1]}"
        __bs_cmd="${BASH_REMATCH[2]}"
    fi
    printf '%s\n%s\n%s\n' "$EPOCHREALTIME" "$__bs_histnum" "$__bs_cmd" > "$_backstory_state_file" 2>/dev/null
}

_backstory_precmd() {
    local __bs_exit=$?

    local __bs_start __bs_histnum __bs_cmd
    { read -r __bs_start; read -r __bs_histnum; read -r __bs_cmd; } < "$_backstory_state_file" 2>/dev/null

    if [[ -z "$__bs_start" || -z "$__bs_histnum" ]]; then
        return $__bs_exit
    fi
    if [[ "$__bs_histnum" == "$_backstory_last_histnum" ]]; then
        return $__bs_exit
    fi
    _backstory_last_histnum="$__bs_histnum"

    if [[ -z "$__bs_cmd" ]]; then
        return $__bs_exit
    fi

    local __bs_end="$EPOCHREALTIME"
    local __bs_start_s=${__bs_start%.*} __bs_start_us=${__bs_start#*.}
    local __bs_end_s=${__bs_end%.*} __bs_end_us=${__bs_end#*.}
    local __bs_dur_ms=$(( ((10#$__bs_end_s - 10#$__bs_start_s) * 1000000 + (10#$__bs_end_us - 10#$__bs_start_us)) / 1000 ))

    ( backstory shell emit --cmd "$__bs_cmd" --cwd "$PWD" --exit "$__bs_exit" --duration-ms "$__bs_dur_ms" >/dev/null 2>&1 </dev/null & ) 2>/dev/null

    return $__bs_exit
}

PS0="${PS0}"'$(_backstory_preexec)'

case "$(declare -p PROMPT_COMMAND 2>/dev/null)" in
    "declare -a"*) PROMPT_COMMAND+=(_backstory_precmd) ;;
    *)
        if [[ -z "${PROMPT_COMMAND:-}" ]]; then
            PROMPT_COMMAND='_backstory_precmd'
        else
            PROMPT_COMMAND="$PROMPT_COMMAND"$'\n''_backstory_precmd'
        fi
        ;;
esac
fi
`
