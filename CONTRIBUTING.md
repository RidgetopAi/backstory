# Contributing to Backstory

House rules for lane workers (human or agent). They are short because the gate
(`make check`) encodes most of them; what is left here is what a test cannot.

## Workflow

1. **Run tests in the FOREGROUND.** `make check` and `make integration` run in
   your terminal, blocking, and you read the exit code. Never background a test
   run and report on it later; a green you did not watch is not a green.
2. **Commit on the branch before any full run.** Uncommitted work does not
   exist. Commit first, then run `make check` / `make integration`; if the run
   is killed, the commit survives.
3. **Every commit that touches a test carries a mutation-probe receipt.** The
   commit body includes a line of the form

       RA-MUTATION-PROBE: <what you broke> -> RED (<failing test>); restored -> GREEN

   A test that has never been seen failing is not known to guard anything.
4. **One package per change.** A commit touches one Go package (plus its test).
   If a change needs two packages, it is two commits.
5. **No new module without a caller.** A dependency enters `go.mod` in the same
   commit as the code that imports it. No "for later".
6. **No prose rule where a test can encode it.** If you find yourself writing a
   rule into a README, a comment, or this file, first ask whether `make check`
   could enforce it. Prefer the gate; delete the prose once it does.

## Gate

`make check` = `fmt-check` + `vet` + `lint` (golangci-lint v2) + `test`
(`go test -race -count=1 ./...`). It must exit 0 on a clean tree and non-zero on
any failure; the merge actor runs exactly this and nothing else.
