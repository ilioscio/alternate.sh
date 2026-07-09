package federation

import (
	"encoding/json"

	"github.com/ilioscio/alternate.sh/internal/assp"
	"github.com/ilioscio/alternate.sh/internal/presence"
)

// WriteResponse sends a JSON control response on ac (used by talk-open handlers).
func WriteResponse(ac *assp.Conn, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return ac.Write(assp.ControlChannel, assp.TypeResponse, 0, b)
}

// TalkChannel is the stream channel a dedicated talk connection uses. Since
// each talk session gets its own ASSP connection in v1, a fixed channel is
// fine; multiplexing several streams over one connection (for A/V) can use the
// full channel space later.
const TalkChannel uint16 = 1

// Talk control verb and messages.
const VerbTalkOpen = "TALK_OPEN"

type TalkOpenRequest struct {
	From   string `json:"from"`   // initiator's username on the calling node
	Target string `json:"target"` // target username on the receiving node
}

type TalkOpenResponse struct {
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason,omitempty"`
}

// RelayRoomToStream bridges a local talk room to a peer over an ASSP stream
// channel. pseudo is a room member standing in for the remote participant:
// bytes the local user(s) type arrive on pseudo.Recv and are forwarded to the
// peer; bytes from the peer are injected back into the room via pseudo.Send so
// the local user sees them.
//
// It runs until either side ends — the local user leaving the room, the peer
// closing the stream, or the connection dropping — then tears both down. This
// same bridge is what a future audio/video channel will reuse: only the bytes
// on the wire differ.
func RelayRoomToStream(pseudo *presence.RoomMember, ac *assp.Conn) {
	readerDone := make(chan struct{})

	// Peer → local: read stream frames and inject them into the room.
	go func() {
		defer close(readerDone)
		for {
			f, err := ac.ReadFrame()
			if err != nil {
				return
			}
			if f.Channel != TalkChannel {
				continue
			}
			switch f.Type {
			case assp.TypeStreamData:
				pseudo.Send(f.Payload)
			case assp.TypeStreamClose:
				return
			}
		}
	}()

	// Local → peer: forward the local user's typed bytes; end when they leave.
	for {
		select {
		case <-readerDone:
			pseudo.Leave()
			ac.Close()
			return
		case ev, ok := <-pseudo.Recv:
			if !ok {
				ac.Write(TalkChannel, assp.TypeStreamClose, 0, nil)
				ac.Close()
				<-readerDone
				return
			}
			switch ev.Kind {
			case presence.EventData:
				if err := ac.Write(TalkChannel, assp.TypeStreamData, assp.FlagDroppable, ev.Data); err != nil {
					pseudo.Leave()
					ac.Close()
					<-readerDone
					return
				}
			case presence.EventLeave:
				// The local participant left the talk.
				ac.Write(TalkChannel, assp.TypeStreamClose, 0, nil)
				pseudo.Leave()
				ac.Close()
				<-readerDone
				return
			}
		}
	}
}
