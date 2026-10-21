package sync

import (
	"errors"
	"fmt"

	"github.com/RidgetopAi/backstory/internal/store"
)

// Outcome is the result of one sync attempt with one peer.
type Outcome struct {
	Peer   store.SyncPeerRow
	Result string // store.SyncResultOK | Unreachable | Error | Paused
	Detail string // one plain line; empty when ok
	// Pulled and Pushed count rows imported here and rows the peer imported.
	Pulled, Pushed int
	// WouldPull and WouldPush are a dry run's counts.
	WouldPull, WouldPush int
}

// OK reports a fully successful sync.
func (o Outcome) OK() bool { return o.Result == store.SyncResultOK }

// Syncer syncs this store with paired peers over a Transport.
type Syncer struct {
	Store     *store.Store
	Identity  store.Identity
	Transport Transport
	Options   Options
}

// Pair runs the hello with host and records the peer. It refuses this device
// and a peer on another proto. The returned error is one plain line.
func (y *Syncer) Pair(host string) (PeerInfo, error) {
	sess, err := y.Transport.Open(host)
	if err != nil {
		return PeerInfo{}, err
	}
	info, herr := Hello(y.Store, sess.Reader(), sess.Writer())
	exit := sess.Finish()
	if herr != nil {
		_, detail := y.classify(herr, exit)
		return PeerInfo{}, errors.New(detail)
	}
	if exit.Code != 0 {
		_, detail := ClassifyExit(exit)
		return PeerInfo{}, errors.New(detail)
	}
	if err := y.Store.PairSyncPeer(info.DeviceID, info.DeviceName, host); err != nil {
		if errors.Is(err, store.ErrPairSelf) {
			return PeerInfo{}, fmt.Errorf("%s is this device (%s); a device cannot be paired with itself", host, info.DeviceName)
		}
		return PeerInfo{}, err
	}
	return info, nil
}

// SyncPeer pulls from then pushes to one peer, persists the cursors for every
// batch that committed, and appends one sync_attempts row (none for a dry
// run, which changes nothing).
func (y *Syncer) SyncPeer(p store.SyncPeerRow) Outcome {
	out := Outcome{Peer: p, Result: store.SyncResultOK}
	opt := y.Options
	opt.ExpectPeerID = p.DeviceID

	var rep Report
	var err error
	var exit Exit
	sess, oerr := y.Transport.Open(p.SSHHost)
	if oerr != nil {
		err = oerr
		exit = Exit{Code: -1}
	} else {
		rep, err = Run(y.Store, y.Identity, Cursors{Pull: p.PullCursor, Push: p.PushCursor}, sess.Reader(), sess.Writer(), opt)
		exit = sess.Finish()
	}
	out.Pulled, out.Pushed = rep.Pulled.Imported(), rep.Pushed.Imported()
	out.WouldPull, out.WouldPush = rep.WouldPull, rep.WouldPush

	switch {
	case oerr != nil:
		out.Result, out.Detail = store.SyncResultError, oerr.Error()
	case err != nil:
		out.Result, out.Detail = y.classify(err, exit)
	case exit.Code != 0:
		out.Result, out.Detail = ClassifyExit(exit)
	case rep.Paused:
		out.Result, out.Detail = store.SyncResultPaused, "capture is off on this device, so nothing was imported from "+p.Label()
	case rep.PeerPaused:
		out.Result, out.Detail = store.SyncResultPaused, "capture is off on "+p.Label()+", so it imported nothing"
	}

	if opt.DryRun {
		return out
	}
	// Cursors cover only batches whose import committed, so they are saved
	// even when the sync failed partway.
	if rep.Cursors != (Cursors{Pull: p.PullCursor, Push: p.PushCursor}) {
		if serr := y.Store.SetSyncCursors(p.DeviceID, rep.Cursors.Pull, rep.Cursors.Push); serr != nil {
			out.Result, out.Detail = store.SyncResultError, serr.Error()
		}
	}
	if aerr := y.Store.AppendSyncAttempt(store.SyncAttempt{
		Peer: p.Label(), DeviceID: p.DeviceID, Result: out.Result, Detail: out.Detail,
		Pulled: out.Pulled, Pushed: out.Pushed,
	}); aerr != nil && out.OK() {
		out.Result, out.Detail = store.SyncResultError, aerr.Error()
	}
	return out
}

// classify maps a failed Run (or Hello) and the peer process's exit to a
// result and reason. What the peer said over the protocol wins; a process that
// died on its own is classified by its exit and stderr; anything else (an
// import failure here, a broken stream) is an error.
func (y *Syncer) classify(err error, exit Exit) (result, detail string) {
	var pm *ProtoMismatchError
	var re *RemoteError
	var pi *PeerIdentityError
	switch {
	case errors.As(err, &pm):
		return store.SyncResultError, fmt.Sprintf("remote backstory speaks sync proto %d, this one %d (proto %d vs %d)", pm.Remote, pm.Local, pm.Remote, pm.Local)
	case errors.As(err, &re):
		return store.SyncResultError, fmt.Sprintf("remote refused: %s: %s", re.Code, re.Message)
	case errors.As(err, &pi):
		return store.SyncResultError, pi.Error()
	}
	if exit.Code != 0 {
		return ClassifyExit(exit)
	}
	return store.SyncResultError, err.Error()
}

// SyncAll syncs every enabled peer in peers, in order, and returns one
// Outcome per peer. A failing peer does not stop the others.
func (y *Syncer) SyncAll(peers []store.SyncPeerRow) []Outcome {
	var outs []Outcome
	for _, p := range peers {
		if !p.Enabled {
			continue
		}
		outs = append(outs, y.SyncPeer(p))
	}
	return outs
}

// FormatFailure is the one plain line `sync` prints for a failing peer.
func (o Outcome) FormatFailure() string {
	return fmt.Sprintf("sync: %s %s: %s", o.Peer.Label(), o.Result, o.Detail)
}
