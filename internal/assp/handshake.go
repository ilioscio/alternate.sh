package assp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
)

// ProtocolVersion is the ASSP wire version. Both peers must agree.
const ProtocolVersion = 1

const nonceLen = 32

// Control-channel messages are JSON. Control traffic is low-rate (handshake,
// presence queries, sync), so clarity beats compactness; stream channels carry
// raw binary for the hot media paths.

type helloMsg struct {
	Version int    `json:"v"`
	Node    string `json:"node"`
	Nonce   []byte `json:"nonce"`
}

type authMsg struct {
	MAC []byte `json:"mac"`
}

// SecretFunc resolves the shared secret for a peer node name, reporting whether
// that peer is known/permitted. The handshake rejects unknown peers.
type SecretFunc func(peerNode string) (secret string, ok bool)

// Handshake performs mutual authentication over conn and returns the
// authenticated peer's node name. dialer must be true on the side that
// initiated the connection and false on the accepting side. Both peers prove
// possession of the shared secret via an HMAC over both nonces and both node
// names, with per-direction domain separation so a proof can't be reflected.
//
// binding is optional TLS channel-binding material (see ChannelBinding). When
// both sides derive it from their TLS session and mix it into the MAC, the
// handshake authenticates the TLS channel itself: a middlebox terminating two
// separate TLS sessions gets different keying material on each leg, so its
// relayed proofs cannot verify. This is what makes self-signed node certs
// safe — trust comes from the peering secret, not a CA.
func Handshake(conn *Conn, localNode string, secretFor SecretFunc, dialer bool, binding []byte) (string, error) {
	localNonce := make([]byte, nonceLen)
	if _, err := rand.Read(localNonce); err != nil {
		return "", err
	}

	// Exchange HELLO. Dialer speaks first to avoid a deadlock.
	var peer helloMsg
	sendHello := func() error {
		return writeControl(conn, TypeHello, helloMsg{Version: ProtocolVersion, Node: localNode, Nonce: localNonce})
	}
	recvHello := func() error {
		if err := readControl(conn, TypeHello, &peer); err != nil {
			return err
		}
		if peer.Version != ProtocolVersion {
			return fmt.Errorf("assp: protocol version mismatch: peer=%d local=%d", peer.Version, ProtocolVersion)
		}
		if peer.Node == "" || len(peer.Nonce) != nonceLen {
			return fmt.Errorf("assp: malformed hello")
		}
		return nil
	}
	if dialer {
		if err := sendHello(); err != nil {
			return "", err
		}
		if err := recvHello(); err != nil {
			return "", err
		}
	} else {
		if err := recvHello(); err != nil {
			return "", err
		}
		if err := sendHello(); err != nil {
			return "", err
		}
	}

	secret, ok := secretFor(peer.Node)

	// The AUTH phase is strictly alternating — dialer writes, listener reads,
	// listener writes, dialer reads — so neither side ever blocks writing
	// while the other is also writing (a real deadlock on unbuffered links,
	// and fragile even with kernel buffers). The listener therefore always
	// consumes the dialer's AUTH frame before sending its verdict, even when
	// it already knows it will reject.
	if dialer {
		if !ok {
			conn.Write(ControlChannel, TypeError, 0, []byte("unknown peer"))
			return "", fmt.Errorf("assp: unknown peer %q", peer.Node)
		}
		clientMAC := computeMAC(secret, "client", localNode, peer.Node, localNonce, peer.Nonce, binding)
		serverMAC := computeMAC(secret, "server", localNode, peer.Node, localNonce, peer.Nonce, binding)
		if err := writeControl(conn, TypeAuth, authMsg{MAC: clientMAC}); err != nil {
			return "", err
		}
		var got authMsg
		if err := readControl(conn, TypeAuth, &got); err != nil {
			return "", err
		}
		if !hmac.Equal(got.MAC, serverMAC) {
			return "", fmt.Errorf("assp: peer authentication failed")
		}
		return peer.Node, nil
	}

	// Listener: consume the dialer's AUTH (or surface its error) first.
	var got authMsg
	if err := readControl(conn, TypeAuth, &got); err != nil {
		return "", err
	}
	if !ok {
		conn.Write(ControlChannel, TypeError, 0, []byte("unknown peer"))
		return "", fmt.Errorf("assp: unknown peer %q", peer.Node)
	}
	clientMAC := computeMAC(secret, "client", peer.Node, localNode, peer.Nonce, localNonce, binding)
	serverMAC := computeMAC(secret, "server", peer.Node, localNode, peer.Nonce, localNonce, binding)
	if !hmac.Equal(got.MAC, clientMAC) {
		conn.Write(ControlChannel, TypeError, 0, []byte("authentication failed"))
		return "", fmt.Errorf("assp: peer authentication failed")
	}
	if err := writeControl(conn, TypeAuth, authMsg{MAC: serverMAC}); err != nil {
		return "", err
	}
	return peer.Node, nil
}

// computeMAC binds the shared secret to both node names, both nonces, and the
// TLS channel binding, in the canonical (client, server) order, with a
// direction label distinguishing the dialer's proof from the accepter's so one
// side's proof cannot be reflected back at it.
func computeMAC(secret, dir, clientNode, serverNode string, clientNonce, serverNonce, binding []byte) []byte {
	m := hmac.New(sha256.New, []byte(secret))
	io.WriteString(m, "assp/1 "+dir+"\n")
	io.WriteString(m, clientNode+"\n"+serverNode+"\n")
	m.Write(clientNonce)
	m.Write(serverNonce)
	m.Write(binding)
	return m.Sum(nil)
}

// ChannelBinding derives per-session keying material from an established TLS
// connection for use as the Handshake binding. Both endpoints of the same TLS
// session derive identical bytes; distinct sessions (e.g. the two legs of a
// TLS-terminating middlebox) derive different byt

func writeControl(conn *Conn, typ uint8, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return conn.Write(ControlChannel, typ, 0, b)
}

func readControl(conn *Conn, wantType uint8, v any) error {
	f, err := conn.ReadFrame()
	if err != nil {
		return err
	}
	if f.Channel != ControlChannel {
		return fmt.Errorf("assp: expected control frame, got channel %d", f.Channel)
	}
	if f.Type == TypeError {
		return fmt.Errorf("assp: peer error: %s", f.Payload)
	}
	if f.Type != wantType {
		return fmt.Errorf("assp: expected frame type 0x%02x, got 0x%02x", wantType, f.Type)
	}
	return json.Unmarshal(f.Payload, v)
}
