package main

import (
	"fmt"
	"io"

	bsync "github.com/RidgetopAi/backstory/internal/sync"
)

// runSyncServe is `backstory sync-serve`: the server side of sync protocol v1
// over stdin/stdout. A paired device runs it (over ssh) to pull this store's
// changes and push its own. It runs as the human, like every other write that
// is not an agent's: the socket API has no import.
func runSyncServe(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 0 {
		_, _ = fmt.Fprintln(stderr, "backstory sync-serve: usage: backstory sync-serve (speaks sync protocol v1 on stdin/stdout)")
		return 2
	}
	st, code := openStoreFor("sync-serve", stderr)
	if st == nil {
		return code
	}
	defer func() { _ = st.Close() }()
	if err := bsync.Serve(st, humanIdentity, stdin, stdout, bsync.Options{}); err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory sync-serve:", err)
		return 1
	}
	return 0
}
