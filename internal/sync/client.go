package sync

import (
	"fmt"
	"io"

	"github.com/RidgetopAi/backstory/internal/store"
)

// Cursors are the two feed positions a pair of devices has reached: Pull in
// the peer's feed, Push in the local one. Zero starts from the beginning;
// replaying from zero is safe because import is idempotent.
type Cursors struct {
	Pull int64
	Push int64
}

// Report is what one Run did.
type Report struct {
	PeerDeviceID   string
	PeerDeviceName string
	// Pulled sums the local imports; Pushed sums the peer's acks.
	Pulled store.SyncImportResult
	Pushed store.SyncImportResult
	// Paused: capture is off here, so nothing was imported from the peer.
	Paused bool
	// PeerPaused: capture is off on the peer, so it imported nothing.
	PeerPaused bool
	// Cursors to resume from; they advance only past what was applied.
	Cursors Cursors
}

// Run is the client side of one sync: hello, pull and import the peer's
// changes, then push the local ones. Imports run as id, which must be human.
// On a ProtoMismatchError nothing has been imported.
func Run(st *store.Store, id store.Identity, from Cursors, r io.Reader, w io.Writer, opt Options) (Report, error) {
	rep := Report{Cursors: from}
	c := newConn(r, w)
	self, err := st.SelfDevice()
	if err != nil {
		return rep, err
	}
	if err := c.send(message{Type: typeHello, Proto: ProtoVersion, DeviceID: self.ID, DeviceName: self.Name}); err != nil {
		return rep, err
	}
	hello, err := c.recv()
	if err != nil {
		return rep, fmt.Errorf("sync: waiting for hello: %w", err)
	}
	switch {
	case hello.Type == typeError:
		return rep, remoteErr(hello)
	case hello.Type != typeHello:
		return rep, errUnexpected
	case hello.Proto != ProtoVersion:
		_ = c.send(message{Type: typeBye})
		return rep, &ProtoMismatchError{Local: ProtoVersion, Remote: hello.Proto}
	case hello.DeviceID == "":
		return rep, fmt.Errorf("sync: peer hello without device_id")
	}
	rep.PeerDeviceID, rep.PeerDeviceName = hello.DeviceID, hello.DeviceName
	peer := store.SyncPeer{DeviceID: hello.DeviceID}

	for !rep.Paused {
		if err := c.send(message{Type: typePull, Since: rep.Cursors.Pull}); err != nil {
			return rep, err
		}
		m, err := c.recv()
		if err != nil {
			return rep, fmt.Errorf("sync: waiting for changes: %w", err)
		}
		if m.Type == typeError {
			return rep, remoteErr(m)
		}
		if m.Type != typeChanges {
			return rep, errUnexpected
		}
		res, err := st.ImportSyncChanges(id, peer, m.Changes)
		if err != nil {
			return rep, err
		}
		if res.Paused {
			rep.Paused = true
			break
		}
		addResult(&rep.Pulled, res)
		rep.Cursors.Pull = m.Next
		if !m.More {
			break
		}
	}

	for {
		changes, next, more, err := st.ChangesSince(rep.Cursors.Push, opt.batch())
		if err != nil {
			return rep, err
		}
		if err := c.send(message{Type: typePush, Changes: changes}); err != nil {
			return rep, err
		}
		m, err := c.recv()
		if err != nil {
			return rep, fmt.Errorf("sync: waiting for ack: %w", err)
		}
		if m.Type == typeError {
			return rep, remoteErr(m)
		}
		if m.Type != typeAck || m.Result == nil {
			return rep, errUnexpected
		}
		if m.Result.Paused {
			rep.PeerPaused = true
			break
		}
		addResult(&rep.Pushed, *m.Result)
		rep.Cursors.Push = next
		if !more {
			break
		}
	}
	return rep, c.send(message{Type: typeBye})
}

func addResult(dst *store.SyncImportResult, r store.SyncImportResult) {
	dst.Records += r.Records
	dst.Tombstones += r.Tombstones
	dst.Edges += r.Edges
	dst.Sessions += r.Sessions
	dst.EdgesDeferred += r.EdgesDeferred
	dst.SkippedNoMatch += r.SkippedNoMatch
}
