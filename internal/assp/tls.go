package assp

import (
	"crypto/tls"
	"fmt"
)

// ChannelBinding derives channel-binding material from an established TLS
// connection, to be mixed into the handshake MAC. It uses RFC 5705 keying
// material exported under a fixed label, which is unique per TLS session. Two
// legs of a man-in-the-middle (who terminates TLS separately with each peer)
// produce different material, so a relayed authentication proof won't verify.
//
// Returns nil if conn is not a completed TLS connection (e.g. plaintext dev
// links or in-memory test pipes); the handshake still authenticates via the
// shared secret, just without channel binding.
func ChannelBinding(conn interface{}) []byte {
	tc, ok := conn.(*tls.Conn)
	if !ok {
		return nil
	}
	st := tc.ConnectionState()
	if !st.HandshakeComplete {
		return nil
	}
	material, err := st.ExportKeyingMaterial("EXPORTER-ASSP-channel-binding", nil, 32)
	if err != nil {
		return nil
	}
	return material
}

// ServerTLSConfig returns a TLS config for the ASSP listener using the given
// certificate/key. Node certs are typically self-signed — trust comes from the
// peering secret plus channel binding, not a CA — so no client cert is required.
func ServerTLSConfig(certFile, keyFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("assp: load node cert: %w", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// ClientTLSConfig returns a TLS config for dialing a peer. Certificate
// verification is skipped because node certs are self-signed; authentication is
// provided by the ASSP handshake (shared secret + channel binding), not PKI.
func ClientTLSConfig() *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true, // node identity is proven by the ASSP handshake
		MinVersion:         tls.VersionTLS12,
	}
}
