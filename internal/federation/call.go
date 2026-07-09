package federation

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ilioscio/alternate.sh/internal/assp"
	"github.com/ilioscio/alternate.sh/internal/av"
	"github.com/ilioscio/alternate.sh/internal/calls"
	"github.com/ilioscio/alternate.sh/internal/presence"
)

// Cross-node calls (DESIGN.md §9.6–9.7). CALL_OPEN mirrors TALK_OPEN — a
// dedicated authenticated connection per call — with two differences: the
// response is deferred until a human answers (or the ring times out), and
// the stream is two channels instead of one, so a congested relay can shed
// video while audio flows.

// VerbCallOpen negotiates a cross-node call on a dedicated connection.
const VerbCallOpen = "CALL_OPEN"

// Stream channels on a call connection. (Channel 1 is talk's.)
const (
	CallVideoChannel uint16 = 2
	CallAudioChannel uint16 = 3
)

// CallOpenRequest is the caller node's proposal.
type CallOpenRequest struct {
	From   string       `json:"from"`   // caller's username on the initiating node
	Target string       `json:"target"` // callee's username on the receiving node
	Media  string       `json:"media"`  // calls.MediaAV or calls.MediaAudio
	Params calls.Params `json:"params"` // proposed video parameters
}

// CallOpenResponse is the callee node's deferred answer. Params are the
// final negotiated values (the callee node clamps the proposal to its own
// configured ceiling; both sides must honor the result).
type CallOpenResponse struct {
	Accepted bool         `json:"accepted"`
	Reason   string       `json:"reason,omitempty"`
	Params   calls.Params `json:"params"`
}

// InitiateCall dials the peer and sends CALL_OPEN. Unlike talk, it does not
// wait for the response — that arrives when a human answers, up to a ring
// timeout away. Consume the connection via ReadFrames + AwaitCallAnswer.
func InitiateCall(ctx context.Context, localNode string, peer Peer, req CallOpenRequest) (*assp.Conn, error) {
	ac, err := dial(ctx, localNode, peer)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(Request{Verb: VerbCallOpen, Arg: req.From, Target: req.Target, Media: req.Media, Params: &req.Params})
	if err != nil {
		ac.Close()
		return nil, err
	}
	if err := ac.Write(assp.ControlChannel, assp.TypeRequest, 0, b); err != nil {
		ac.Close()
		return nil, err
	}
	return ac, nil
}

// ReadFrames pumps frames from ac into a channel until the connection dies;
// the channel closes on read error. It exists so one goroutine can own the
// connection's read side across signaling and bridging phases — closing ac
// from any other goroutine cleanly unblocks it.
func ReadFrames(ac *assp.Conn) <-chan assp.Frame {
	frames := make(chan assp.Frame)
	go func() {
		defer close(frames)
		for {
			f, err := ac.ReadFrame()
			if err != nil {
				return
			}
			frames <- f
		}
	}()
	return frames
}

// AwaitCallAnswer consumes frames until the peer's CALL_OPEN response.
func AwaitCallAnswer(frames <-chan assp.Frame) (CallOpenResponse, error) {
	for f := range frames {
		if f.Channel != assp.ControlChannel {
			continue // no media is legitimate before the answer; drop strays
		}
		switch f.Type {
		case assp.TypeResponse:
			var resp CallOpenResponse
			if err := json.Unmarshal(f.Payload, &resp); err != nil {
				return CallOpenResponse{}, err
			}
			return resp, nil
		case assp.TypeError:
			return CallOpenResponse{}, fmt.Errorf("federation: peer error: %s", f.Payload)
		}
	}
	return CallOpenResponse{}, fmt.Errorf("federation: connection closed before answer")
}

// RelayCallRoomToStream bridges a local call room to a peer over a call
// connection's stream channels: video on channel 2, audio on channel 3,
// every media frame flagged droppable. pseudo stands in for the remote
// participant; expectSource is the source id the remote party legitimately
// stamps (its packets are dropped otherwise, mirroring the /ws/call ingress
// check). frames must be the connection's ReadFrames channel.
//
// The bridge runs until the call ends — locally (c ended: we close the
// stream), remotely (stream close or connection loss: we end c) — and tears
// everything down on the way out. This is the Phase-5 room-to-stream
// primitive with a media-aware channel map.
func RelayCallRoomToStream(c *calls.Call, pseudo *presence.RoomMember, ac *assp.Conn, frames <-chan assp.Frame, expectSource uint8) {
	// The call ending for any reason must unblock every path below.
	go func() {
		<-c.Ended()
		ac.Write(CallVideoChannel, assp.TypeStreamClose, 0, nil) // best-effort goodbye
		ac.Close()
	}()

	for {
		select {
		case f, ok := <-frames:
			if !ok {
				// Peer connection died (or was closed by the ender above).
				c.End("connection to peer lost")
				pseudo.Leave()
				return
			}
			switch f.Type {
			case assp.TypeStreamClose:
				c.End("peer hung up")
				pseudo.Leave()
				ac.Close()
				return
			case assp.TypeStreamData:
				if f.Channel != CallVideoChannel && f.Channel != CallAudioChannel {
					continue
				}
				p, err := av.ParsePacket(f.Payload)
				if err != nil || p.Source != expectSource {
					continue
				}
				pseudo.Send(f.Payload)
			}

		case ev, ok := <-pseudo.Recv:
			if !ok {
				// Our own membership was torn down; end and close.
				c.End("call closed")
				ac.Close()
				return
			}
			if ev.Kind != presence.EventData || len(ev.Data) == 0 {
				continue
			}
			ch := CallVideoChannel
			if av.Kind(ev.Data[0]) == av.KindAudio {
				ch = CallAudioChannel
			}
			if err := ac.Write(ch, assp.TypeStreamData, assp.FlagDroppable, ev.Data); err != nil {
				c.End("connection to peer lost")
				pseudo.Leave()
				return
			}
		}
	}
}
