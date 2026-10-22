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
	return y.PairAs(host, PairRequest{})
}

// PairAs is Pair, and with req.Pair also has the peer record this device and
// check its way back. A failed way back is PeerInfo.BackError, not an error.
func (y *Syncer) PairAs(host string, req PairRequest) (PeerInfo, error) {
	sess, err := y.Transport.Open(host)
	if err != nil {
		return PeerInfo{}, err
	}
	info, herr := Hello(y.Store, sess.Reader(), sess.Writer(), req)
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

// BackHostFor is the host a pairing peer records for reaching its client:
// the client's own override, else user@address from the connection the server
// saw. It is empty when neither is known.
func BackHostFor(req message, clientAddr string) string {
	if req.BackHost != "" {
		return req.BackHost
	}
	if req.BackUser == "" || clientAddr == "" {
		return ""
	}
	return req.BackUser + "@" + clientAddr
}

// BackCandidates are the hosts a pairing peer tries, in order, for reaching
// its client. An explicit override is the only candidate. Otherwise they are
// user@name for each tailnet name the client advertised (MagicDNS FQDN first,
// then the short name), then user@address from the connection the server saw.
// Empty, duplicate and invalid hosts are dropped.
func BackCandidates(req message, clientAddr string) []string {
	if req.BackHost != "" {
		return []string{req.BackHost}
	}
	if req.BackUser == "" {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, n := range append(append([]string{}, req.BackNames...), clientAddr) {
		if n == "" {
			continue
		}
		h := req.BackUser + "@" + n
		if seen[h] || ValidHost(h) != nil {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
}

// pairClient records a pairing client as a peer of st and checks the way back
// to it. The returned error refuses the pairing (self, bad host); backErr is
// the one plain line for a way back that does not work, in which case the
// peer row is still written and a failed attempt is logged so status shows it.
func pairClient(st *store.Store, hello message, opt Options) (backHost, backErr string, err error) {
	self, serr := st.SelfDevice()
	if serr != nil {
		return "", "", serr
	}
	if hello.DeviceID == self.ID {
		return "", "", fmt.Errorf("%s is this device; a device cannot be paired with itself", hello.DeviceName)
	}
	cands := BackCandidates(hello, opt.ClientAddr)
	if len(cands) == 0 {
		// Nothing to reach it by: remember who it is, pair nothing.
		if rerr := st.RecordDevice(hello.DeviceID, hello.DeviceName); rerr != nil {
			return "", "", rerr
		}
		return "", "it sent no user and the connection address is unknown", nil
	}
	if hello.BackHost != "" {
		if verr := ValidHost(hello.BackHost); verr != nil {
			return "", "", verr
		}
	}
	// Check each candidate in order; the first that answers as the client wins.
	// When none does, the first is recorded and the last failure reported.
	y := &Syncer{Store: st, Transport: opt.Transport}
	host, result, detail := cands[0], "", ""
	for i, c := range cands {
		r, d := y.checkBack(c, hello.DeviceID)
		if i == 0 {
			result, detail = r, d
		}
		if r == store.SyncResultOK {
			host, result, detail = c, r, ""
			break
		}
	}
	// A back registration only creates the row; an existing host stays unless
	// the client named one with --back.
	recorded, kept := host, ""
	if hello.BackHost == "" {
		peers, lerr := st.SyncPeers()
		if lerr != nil {
			return "", "", lerr
		}
		for _, p := range peers {
			if p.DeviceID == hello.DeviceID && p.SSHHost != "" {
				recorded, kept = p.SSHHost, p.SSHHost
			}
		}
	}
	if perr := st.PairSyncPeer(hello.DeviceID, hello.DeviceName, recorded); perr != nil {
		if errors.Is(perr, store.ErrPairSelf) {
			return "", "", fmt.Errorf("%s is this device; a device cannot be paired with itself", hello.DeviceName)
		}
		return "", "", perr
	}
	if result == store.SyncResultOK {
		return recorded, "", nil
	}
	if kept != "" {
		detail += "; this device keeps its existing host " + kept + " for it"
	}
	label := hello.DeviceName
	if label == "" {
		label = host
	}
	_ = st.AppendSyncAttempt(store.SyncAttempt{Peer: label, DeviceID: hello.DeviceID, Result: result, Detail: detail})
	return host, detail, nil
}

// checkBack runs a hello to host and expects device wantID to answer.
func (y *Syncer) checkBack(host, wantID string) (result, detail string) {
	sess, err := y.Transport.Open(host)
	if err != nil {
		return store.SyncResultError, err.Error()
	}
	info, herr := Hello(y.Store, sess.Reader(), sess.Writer(), PairRequest{})
	exit := sess.Finish()
	switch {
	case herr != nil:
		return y.classify(herr, exit)
	case exit.Code != 0:
		return ClassifyExit(exit)
	case info.DeviceID != wantID:
		return store.SyncResultError, (&PeerIdentityError{Want: wantID, Got: info.DeviceID, GotName: info.DeviceName}).Error()
	}
	return store.SyncResultOK, ""
}
