package shell

import (
	"context"
	"strings"
	"time"

	"github.com/ilioscio/alternate.sh/internal/db"
	"github.com/ilioscio/alternate.sh/internal/federation"
)

// talkRemote handles `talk user@host`. It has two modes:
//
//   - Joining: if the federation server has already set up an inbound relay
//     for us from this remote (i.e. someone on the peer initiated), we join the
//     existing bridged room and run the UI.
//   - Initiating: otherwise we dial the peer, negotiate TALK_OPEN, and bridge a
//     local room to the returned connection.
//
// Either way the local user drives the same split-screen UI as local talk; the
// remote participant is represented in the room by a relay stand-in member.
func talkRemote(s *Session, target string) error {
	at := strings.LastIndex(target, "@")
	remoteUser, host := target[:at], target[at+1:]
	if remoteUser == "" || host == "" {
		s.Println("talk: usage: talk user@host")
		return nil
	}
	remoteQualified := target // "user@host"

	// Joining an inbound talk the server already bridged.
	if s.hub.HasIncomingTalk(s.User.Username, remoteQualified) {
		s.hub.RemoveIncomingTalk(s.User.Username, remoteQualified)
		participants := []string{s.User.Username, remoteQualified}
		member, peers, ok := s.hub.Rooms.Join(participants, s.ID, s.User.Username)
		if !ok {
			s.Println("talk: could not join session")
			return nil
		}
		runTalkUI(s, member, peers)
		s.Write([]byte("\x1b[2J\x1b[H"))
		s.Println("[talk session ended]")
		return nil
	}

	// Initiating an outbound talk to a peer node.
	peer, err := db.GetPeer(s.ctx, s.db, host)
	if err != nil {
		s.Printf("talk: %s: not a federation peer\r\n", host)
		return nil
	}
	fp := federation.Peer{
		Node:    peer.Node,
		Address: federation.ResolveAddress(peer.Address, peer.Node, s.cfg.Federation.ASSPPort),
		Secret:  peer.Secret,
	}

	s.Printf("Ringing %s...\r\n", target)
	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	ac, reason, err := federation.InitiateTalk(ctx, s.cfg.Server.Hostname, fp, s.User.Username, remoteUser)
	cancel()
	if err != nil {
		s.Printf("talk: %s: unreachable\r\n", host)
		return nil
	}
	if ac == nil {
		s.Printf("talk: %s declined (%s)\r\n", target, reason)
		return nil
	}

	// Join the local room as ourselves, and add a relay stand-in for the remote.
	participants := []string{s.User.Username, remoteQualified}
	member, _, ok := s.hub.Rooms.Join(participants, s.ID, s.User.Username)
	if !ok {
		ac.Close()
		s.Println("talk: could not set up room")
		return nil
	}
	relaySession := "relay:" + s.User.Username + "->" + remoteQualified
	pseudo, peers, ok := s.hub.Rooms.Join(participants, relaySession, remoteQualified)
	if !ok {
		member.Leave()
		ac.Close()
		s.Println("talk: could not set up relay")
		return nil
	}

	// Bridge the relay stand-in to the connection in the background; the local
	// user drives the UI in the foreground.
	go federation.RelayRoomToStream(pseudo, ac)

	runTalkUI(s, member, peers)

	// Local user left: dropping our membership makes the relay send a stream
	// close to the peer and tear down.
	s.Write([]byte("\x1b[2J\x1b[H"))
	s.Println("[talk session ended]")
	return nil
}
