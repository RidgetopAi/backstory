// Package sync is the replication core of cross-device sync (PLAN.md Phase 8,
// decision 2ac4c54f): protocol v1 over an io.Reader/io.Writer pair, the server
// side (Serve) and the client side (Run). It has no transport; the caller
// supplies the pipe (sync-serve over stdin/stdout, an ssh session, a test's
// in-process pipe).
//
// Protocol v1 is JSON lines. The client opens with hello {proto, device_id,
// device_name}; the server answers hello or a typed error. Then any number of
// pull {since} -> changes {changes, next, more} and push {changes} -> ack, and
// bye. Cursors are positions in the serving store's change feed, never
// timestamps. A peer speaking another proto is refused with a
// *ProtoMismatchError naming both versions, and nothing is imported.
package sync

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/RidgetopAi/backstory/internal/store"
)

// ProtoVersion is the wire protocol this build speaks.
const ProtoVersion = 1

// DefaultBatchSize is how many feed entries one pull or push carries.
const DefaultBatchSize = 500

// MaxLineBytes bounds one protocol line, so a peer cannot make the reader
// buffer without limit.
const MaxLineBytes = 64 << 20

// Message types.
const (
	typeHello   = "hello"
	typePull    = "pull"
	typeChanges = "changes"
	typePush    = "push"
	typeAck     = "ack"
	typeError   = "error"
	typeBye     = "bye"
)

// Error codes carried by an error message.
const (
	CodeProtoMismatch = "proto_mismatch"
	CodeImportRefused = "import_refused"
	CodeBadRequest    = "bad_request"
	CodeInternal      = "internal"
)

// Options tunes a Serve or Run.
type Options struct {
	// BatchSize is the feed entries per pull or push; 0 means DefaultBatchSize.
	BatchSize int
	// DryRun makes Run count what it would pull and push, importing and
	// sending nothing.
	DryRun bool
	// ExpectPeerID, when set, makes Run refuse (before anything is imported)
	// a peer whose hello names another device: cursors belong to one peer.
	ExpectPeerID string
}

// PeerIdentityError is returned when the device that answered is not the one
// paired at that host.
type PeerIdentityError struct {
	Want, Got, GotName string
}

func (e *PeerIdentityError) Error() string {
	return fmt.Sprintf("sync: the host now answers as device %q (%s), not the paired device %s", e.GotName, e.Got, e.Want)
}

func (o Options) batch() int {
	if o.BatchSize > 0 {
		return o.BatchSize
	}
	return DefaultBatchSize
}

// ProtoMismatchError is returned when the peer speaks another protocol.
type ProtoMismatchError struct {
	Local, Remote int
}

func (e *ProtoMismatchError) Error() string {
	return fmt.Sprintf("sync: protocol mismatch: this side speaks proto %d, the peer proto %d", e.Local, e.Remote)
}

// RemoteError is an error the peer reported.
type RemoteError struct {
	Code    string
	Message string
}

func (e *RemoteError) Error() string {
	return fmt.Sprintf("sync: peer error %s: %s", e.Code, e.Message)
}

type wireError struct {
	Code        string `json:"code"`
	Message     string `json:"message,omitempty"`
	ServerProto int    `json:"server_proto,omitempty"`
	ClientProto int    `json:"client_proto,omitempty"`
}

type message struct {
	Type       string                  `json:"type"`
	Proto      int                     `json:"proto,omitempty"`
	DeviceID   string                  `json:"device_id,omitempty"`
	DeviceName string                  `json:"device_name,omitempty"`
	Since      int64                   `json:"since"`
	Next       int64                   `json:"next"`
	More       bool                    `json:"more,omitempty"`
	Changes    []store.SyncChange      `json:"changes,omitempty"`
	Result     *store.SyncImportResult `json:"result,omitempty"`
	Error      *wireError              `json:"error,omitempty"`
}

// conn reads and writes protocol lines.
type conn struct {
	sc  *bufio.Scanner
	enc *json.Encoder
}

func newConn(r io.Reader, w io.Writer) *conn {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), MaxLineBytes)
	return &conn{sc: sc, enc: json.NewEncoder(w)}
}

func (c *conn) send(m message) error {
	if err := c.enc.Encode(m); err != nil {
		return fmt.Errorf("sync: write %s: %w", m.Type, err)
	}
	return nil
}

// recv reads one message; io.EOF at a clean end of input.
func (c *conn) recv() (message, error) {
	if !c.sc.Scan() {
		if err := c.sc.Err(); err != nil {
			return message{}, fmt.Errorf("sync: read: %w", err)
		}
		return message{}, io.EOF
	}
	var m message
	if err := json.Unmarshal(c.sc.Bytes(), &m); err != nil {
		return message{}, fmt.Errorf("sync: malformed message: %w", err)
	}
	return m, nil
}

func remoteErr(m message) error {
	if m.Error == nil {
		return fmt.Errorf("sync: peer sent an error with no body")
	}
	if m.Error.Code == CodeProtoMismatch {
		return &ProtoMismatchError{Local: m.Error.ClientProto, Remote: m.Error.ServerProto}
	}
	return &RemoteError{Code: m.Error.Code, Message: m.Error.Message}
}

var errUnexpected = errors.New("sync: unexpected message from peer")
