package server

import (
	"net/http"
	"slices"

	"github.com/gorilla/websocket"

	"github.com/ilioscio/alternate.sh/internal/av"
	"github.com/ilioscio/alternate.sh/internal/calls"
	"github.com/ilioscio/alternate.sh/internal/presence"
)

// /ws/call is the media-only WebSocket (DESIGN.md §9.4, §9.7). The browser
// attaches it when the terminal's control channel announces a call; every
// binary message is one media packet, relayed verbatim through the call's
// room. The server never decodes media — it checks only the 4-byte header
// (well-formed, correct source id) and fans the bytes out.
//
// Lifecycle: the socket lives exactly as long as the call. Call ends → the
// server closes the socket; socket dies (browser closed, network drop) →
// the call ends. There is no reconnect: calls are ephemeral, like talk.

func (s *WebSocketServer) handleCallWS(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "token required", http.StatusUnauthorized)
		return
	}
	u, err := s.authFn(r.Context(), token)
	if err != nil {
		http.Error(w, "invalid or expired session", http.StatusUnauthorized)
		return
	}

	c := s.hub.Calls.Get(r.URL.Query().Get("call"))
	if c == nil || !c.Involves(u.Username) || !c.Active() {
		// One generic 404: no oracle for probing other users' call IDs.
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	source := calls.SourceCallee
	if c.Caller == u.Username {
		source = calls.SourceCaller
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	conn.SetReadLimit(av.MaxPacketSize)

	member, peers, ok := s.hub.Rooms.JoinID(
		c.RoomID(),
		[]string{c.Caller, c.Callee},
		// RemoteAddr keeps the ID unique even if the same user races two
		// sockets in; the duplicate check below rejects the loser.
		"callws-"+u.Username+"-"+c.ID+"-"+conn.RemoteAddr().String(),
		u.Username,
	)
	if !ok {
		return
	}
	// One media socket per participant: a second tab attaching to the same
	// call would double-deliver every packet.
	if slices.Contains(peers, u.Username) {
		member.Leave()
		return
	}
	defer member.Leave()

	// Writer: room → socket. Exits when our membership closes (call ended or
	// we left); closing the conn below also unblocks the read loop.
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		defer conn.Close()
		for ev := range member.Recv {
			if ev.Kind != presence.EventData {
				continue
			}
			if conn.WriteMessage(websocket.BinaryMessage, ev.Data) != nil {
				return
			}
		}
	}()

	// End watcher: the call ending (either terminal hanging up, ring timeout,
	// peer's socket dying) tears this socket down.
	go func() {
		select {
		case <-c.Ended():
			member.Leave() // closes Recv → writer exits → conn closes
		case <-writerDone:
		}
	}()

	// Reader: socket → room. The source check stops a client stamping its
	// packets with its peer's id (matters once group calls fan out by source).
	for {
		msgType, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}
		if msgType != websocket.BinaryMessage {
			continue
		}
		p, err := av.ParsePacket(msg)
		if err != nil || p.Source != source {
			continue
		}
		member.Send(msg)
	}

	// Socket gone. If the call is still live this was an abnormal drop
	// (closed tab, network); end it so the peer isn't left talking to
	// nothing. End is idempotent — a normal teardown already ended it.
	c.End("media link lost")
	member.Leave()
	<-writerDone
}
