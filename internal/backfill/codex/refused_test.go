package codex

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/RidgetopAi/backstory/internal/store"
)

// purgedSession returns a purged session id: every backfill append to it is
// refused by the store's purge gate.
func purgedSession(t *testing.T, st *store.Store) string {
	t.Helper()
	id, err := st.StartSession(store.StartSessionParams{
		Agent: "codex", CWD: "/x", StartedAt: time.Unix(1700000000, 0), Origin: store.OriginBackfilled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PurgeSessions(store.PurgeScope{SessionID: id}, store.Identity{Kind: store.IdentityHuman}); err != nil {
		t.Fatal(err)
	}
	return id
}

// RA-MUTATION-PROBE: the counter counts a refused append -> RED.
func TestRefusedWritesCountZero(t *testing.T) {
	st := mustOpenStore(t)
	id := purgedSession(t, st)
	lines := []rolloutLine{{Type: "response_item", Timestamp: time.Unix(1700000001, 0),
		Payload: json.RawMessage(`{"type":"function_call","name":"shell","call_id":"c1","arguments":"{}"}`)}}
	n, err := appendToolEvents(st, id, lines, []int{0})
	if err != nil || n != 0 {
		t.Fatalf("appendToolEvents = %d, %v; want 0, nil", n, err)
	}
}
