package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/RidgetopAi/backstory/internal/store"
	bsync "github.com/RidgetopAi/backstory/internal/sync"
)

// runPair is `backstory pair <ssh-host>`: it runs the sync hello over ssh and
// records the peer. `pair --remove <name|host>` disables a peer; the records
// it sent stay.
func runPair(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pair", flag.ContinueOnError)
	remove := fs.Bool("remove", false, "stop syncing with a paired device (its imported records stay)")
	fs.SetOutput(stderr)
	fs.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "usage: backstory pair <ssh-host> | backstory pair --remove <name|host>")
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	ref := fs.Arg(0)
	st, code := openStoreFor("pair", stderr)
	if st == nil {
		return code
	}
	defer func() { _ = st.Close() }()

	if *remove {
		p, err := st.FindSyncPeer(ref)
		if err == nil {
			err = st.DisableSyncPeer(p.DeviceID)
		}
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory pair:", trimStorePrefix(err))
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "%s will no longer sync; the records it sent stay.\n", p.Label())
		return 0
	}

	y := &bsync.Syncer{Store: st, Identity: humanIdentity, Transport: bsync.TransportFromEnv()}
	info, err := y.Pair(ref)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "backstory pair:", trimStorePrefix(err))
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "Paired with %s (%s). Run `backstory sync` to share memory.\n", info.DeviceName, shortDeviceID(info.DeviceID))
	return 0
}

// runSync is `backstory sync [--dry-run] [<peer>]`: for each enabled peer, or
// the named one, pull then push. Every attempt is logged; the exit is non-zero
// with one line per peer that did not finish ok.
func runSync(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "print what would be pulled and pushed; change nothing")
	quiet := fs.Bool("quiet", false, "print nothing on success; on failure only the per-peer failure lines")
	fs.SetOutput(stderr)
	fs.Usage = func() { _, _ = fmt.Fprintln(stderr, "usage: backstory sync [--dry-run] [--quiet] [<peer>]") }
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 1 {
		fs.Usage()
		return 2
	}
	st, code := openStoreFor("sync", stderr)
	if st == nil {
		return code
	}
	defer func() { _ = st.Close() }()

	// One sync at a time: a manual run and the timer never overlap. A dry run
	// changes nothing, so it does not wait.
	if !*dry {
		dbPath, err := storePath()
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "backstory sync: %v\n", err)
			return 1
		}
		lock, err := bsync.AcquireLock(filepath.Dir(dbPath))
		if errors.Is(err, bsync.ErrBusy) {
			if !*quiet {
				_, _ = fmt.Fprintln(stdout, "sync: busy: another sync is already running; nothing was attempted")
			}
			return 0
		}
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "backstory sync: %v\n", err)
			return 1
		}
		defer lock.Release()
	}

	var peers []store.SyncPeerRow
	if fs.NArg() == 1 {
		p, err := st.FindSyncPeer(fs.Arg(0))
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory sync:", trimStorePrefix(err))
			return 1
		}
		if !p.Enabled {
			_, _ = fmt.Fprintf(stderr, "backstory sync: %s was removed; run `backstory pair %s` to sync with it again\n", p.Label(), p.SSHHost)
			return 1
		}
		peers = []store.SyncPeerRow{p}
	} else {
		all, err := st.SyncPeers()
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "backstory sync:", err)
			return 1
		}
		peers = all
	}

	y := &bsync.Syncer{Store: st, Identity: humanIdentity, Transport: bsync.TransportFromEnv(), Options: bsync.Options{DryRun: *dry}}
	outs := y.SyncAll(peers)
	if len(outs) == 0 {
		if *quiet {
			return 0
		}
		_, _ = fmt.Fprintln(stdout, "No paired devices. Run `backstory pair <ssh-host>` first.")
		return 0
	}
	failed := false
	for _, o := range outs {
		switch {
		case !o.OK():
			failed = true
			_, _ = fmt.Fprintln(stderr, o.FormatFailure())
		case *quiet:
		case *dry:
			_, _ = fmt.Fprintf(stdout, "%s: would pull %d, would push %d (dry run; nothing changed)\n", o.Peer.Label(), o.WouldPull, o.WouldPush)
		default:
			_, _ = fmt.Fprintf(stdout, "%s: pulled %d, pushed %d\n", o.Peer.Label(), o.Pulled, o.Pushed)
		}
	}
	if failed {
		return 1
	}
	return 0
}

// trimStorePrefix drops the "store: " package prefix from an error shown to a person.
func trimStorePrefix(err error) string {
	s := err.Error()
	if errors.Is(err, store.ErrNoSuchPeer) || errors.Is(err, store.ErrPairSelf) {
		if len(s) > 7 && s[:7] == "store: " {
			return s[7:]
		}
	}
	return s
}
