package sync

import (
	"errors"
	"fmt"
	"io"

	"github.com/RidgetopAi/backstory/internal/store"
)

// Serve runs the server side of protocol v1 until the peer says bye or closes
// the stream. Pushed changes are imported with id, so a server run with
// anything but a human identity refuses every push (sync-serve is a CLI
// command run by the human; the socket API has no import). A peer with another
// proto is refused with a ProtoMismatchError before anything is read or
// imported.
func Serve(st *store.Store, id store.Identity, r io.Reader, w io.Writer, opt Options) error {
	c := newConn(r, w)
	self, err := st.SelfDevice()
	if err != nil {
		return err
	}
	hello, err := c.recv()
	if err != nil {
		return err
	}
	if hello.Type != typeHello {
		_ = c.send(message{Type: typeError, Error: &wireError{Code: CodeBadRequest, Message: "expected hello"}})
		return errUnexpected
	}
	if hello.Proto != ProtoVersion {
		_ = c.send(message{Type: typeError, Error: &wireError{Code: CodeProtoMismatch,
			Message:     fmt.Sprintf("server speaks proto %d, client proto %d", ProtoVersion, hello.Proto),
			ServerProto: ProtoVersion, ClientProto: hello.Proto}})
		return &ProtoMismatchError{Local: ProtoVersion, Remote: hello.Proto}
	}
	if hello.DeviceID == "" {
		_ = c.send(message{Type: typeError, Error: &wireError{Code: CodeBadRequest, Message: "hello without device_id"}})
		return errUnexpected
	}
	reply := message{Type: typeHello, Proto: ProtoVersion, DeviceID: self.ID, DeviceName: self.Name}
	if hello.Pair {
		host, backErr, perr := pairClient(st, hello, opt)
		if perr != nil {
			_ = c.send(message{Type: typeError, Error: &wireError{Code: CodeBadRequest, Message: perr.Error()}})
			return perr
		}
		reply.BackHost, reply.BackError = host, backErr
	} else if err := st.RecordDevice(hello.DeviceID, hello.DeviceName); err != nil {
		_ = c.send(message{Type: typeError, Error: &wireError{Code: CodeInternal, Message: err.Error()}})
		return err
	}
	if err := c.send(reply); err != nil {
		return err
	}
	peer := store.SyncPeer{DeviceID: hello.DeviceID}

	for {
		m, err := c.recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		switch m.Type {
		case typeBye:
			return nil
		case typePull:
			changes, next, more, err := st.ChangesSince(m.Since, opt.batch())
			if err != nil {
				_ = c.send(message{Type: typeError, Error: &wireError{Code: CodeInternal, Message: err.Error()}})
				return err
			}
			if err := c.send(message{Type: typeChanges, Changes: changes, Next: next, More: more}); err != nil {
				return err
			}
		case typePush:
			res, err := st.ImportSyncChanges(id, peer, m.Changes)
			switch {
			case errors.Is(err, store.ErrSyncImportRequiresHuman):
				if serr := c.send(message{Type: typeError, Error: &wireError{Code: CodeImportRefused, Message: err.Error()}}); serr != nil {
					return serr
				}
			case err != nil:
				_ = c.send(message{Type: typeError, Error: &wireError{Code: CodeInternal, Message: err.Error()}})
				return err
			default:
				if err := c.send(message{Type: typeAck, Result: &res}); err != nil {
					return err
				}
			}
		default:
			_ = c.send(message{Type: typeError, Error: &wireError{Code: CodeBadRequest, Message: "unknown message " + m.Type}})
			return errUnexpected
		}
	}
}
