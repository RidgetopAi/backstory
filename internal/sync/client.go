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
	// WouldPull and WouldPush are the change counts a DryRun found; nothing
	// was imported or sent.
	WouldPull int
	WouldPush int
	// Cursors to resume from; they advance only past what was applied.
	Cursors Cursors
}

// Run is the client side of one sync: hello, pull and import the peer's
// changes, then push the local ones. Imports run as id, which must be human.
// On a ProtoMismatchError nothing has been imported.
func Run(st *store.Store, id store.Identity, from Cursors, r io.Reader, w io.Writer, opt Options) (Report, error) {
	rep := Report{Cursors: from}
	c := newConn(r, w)
	hello, err := handshake(c, st, PairRequest{})
	if err != nil {
		return rep, err
	}
	rep.PeerDeviceID, rep.PeerDeviceName = hello.DeviceID, hello.DeviceName
	if opt.ExpectPeerID != "" && hello.DeviceID != opt.ExpectPeerID {
		_ = c.send(message{Type: typeBye})
		return rep, &PeerIdentityError{Want: opt.ExpectPeerID, Got: hello.DeviceID, GotName: hello.DeviceName}
	}
	peer := store.SyncPeer{DeviceID: hello.DeviceID}

	pullPos, pushPos := rep.Cursors.Pull, rep.Cursors.Push // dry-run positions; the cursors stay put
	for !rep.Paused {
		if err := c.send(message{Type: typePull, Since: pullPos}); err != nil {
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
		if opt.DryRun {
			rep.WouldPull += len(m.Changes)
			pullPos = m.Next
			if !m.More {
				break
			}
			continue
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
		pullPos = m.Next
		if !m.More {
			break
		}
	}

	for {
		changes, next, more, err := st.ChangesSince(pushPos, opt.batch())
		if err != nil {
			return rep, err
		}
		if opt.DryRun {
			rep.WouldPush += len(changes)
			pushPos = next
			if !more {
				break
			}
			continue
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
		pushPos = next
		if !more {
			break
		}
	}
	return rep, c.send(message{Type: typeBye})
}

// handshake sends this store's hello and validates the peer's answer.
func handshake(c *conn, st *store.Store, req PairRequest) (message, error) {
	self, err := st.SelfDevice()
	if err != nil {
		return message{}, err
	}
	if err := c.send(message{Type: typeHello, Proto: ProtoVersion, DeviceID: self.ID, DeviceName: self.Name,
		Pair: req.Pair, BackUser: req.User, BackHost: req.Host, BackNames: req.Names}); err != nil {
		return message{}, err
	}
	hello, err := c.recv()
	if err != nil {
		return message{}, fmt.Errorf("sync: waiting for hello: %w", err)
	}
	switch {
	case hello.Type == typeError:
		return message{}, remoteErr(hello)
	case hello.Type != typeHello:
		return message{}, errUnexpected
	case hello.Proto != ProtoVersion:
		_ = c.send(message{Type: typeBye})
		return message{}, &ProtoMismatchError{Local: ProtoVersion, Remote: hello.Proto}
	case hello.DeviceID == "":
		return message{}, fmt.Errorf("sync: peer hello without device_id")
	}
	return hello, nil
}

// PairRequest asks the answering side to record this device as its peer.
type PairRequest struct {
	// Pair turns the request on.
	Pair bool
	// User is this device's local user, for the default back host.
	User string
	// Host overrides the back host the peer would derive from its client address.
	Host string
	// Names are this device's tailnet names, FQDN first, advertised so the peer
	// can reach it by name; unused when Host is set.
	Names []string
}

// PeerInfo is who answered a Hello.
type PeerInfo struct {
	DeviceID   string
	DeviceName string
	// BackError is why a pairing peer could not reach this device back; empty
	// when it could (or for a plain Hello).
	BackError string
	// BackHost is the host the peer recorded for reaching this device.
	BackHost string
}

// Hello runs only the handshake, then says bye: it is how `pair` learns the
// peer's identity. A peer on another proto yields a *ProtoMismatchError.
// With req.Pair the peer also records this device and checks the way back.
func Hello(st *store.Store, r io.Reader, w io.Writer, req PairRequest) (PeerInfo, error) {
	c := newConn(r, w)
	hello, err := handshake(c, st, req)
	if err != nil {
		return PeerInfo{}, err
	}
	_ = c.send(message{Type: typeBye})
	return PeerInfo{DeviceID: hello.DeviceID, DeviceName: hello.DeviceName, BackError: hello.BackError, BackHost: hello.BackHost}, nil
}

func addResult(dst *store.SyncImportResult, r store.SyncImportResult) {
	dst.Records += r.Records
	dst.Tombstones += r.Tombstones
	dst.Edges += r.Edges
	dst.Sessions += r.Sessions
	dst.EdgesDeferred += r.EdgesDeferred
	dst.SkippedNoMatch += r.SkippedNoMatch
}
