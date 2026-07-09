// Package federation implements node-to-node presence, finger, and (later)
// talk relay over ASSP. Control queries are request/response JSON on the ASSP
// control channel; the transport, framing, and mutual auth live in
// internal/assp.
package federation

// Control verbs.
const (
	VerbWho    = "WHO"
	VerbFinger = "FINGER"
)

// Request is a control-channel query from one node to another.
type Request struct {
	ID   uint32 `json:"id"`
	Verb string `json:"verb"`
	Arg  string `json:"arg,omitempty"`
}

// PresenceEntry is one logged-in user as reported by a node's WHO.
type PresenceEntry struct {
	Username string `json:"user"`
	TTY      string `json:"tty"`
	LoginAt  int64  `json:"login"` // unix seconds
	From     string `json:"from"`
	State    string `json:"state"`
}

// WhoResponse is a node's answer to WHO.
type WhoResponse struct {
	Node  string          `json:"node"`
	Users []PresenceEntry `json:"users"`
}

// FingerResponse is a node's answer to FINGER <user>.
type FingerResponse struct {
	Found     bool   `json:"found"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	Office    string `json:"office,omitempty"`
	Plan      string `json:"plan,omitempty"`
	Project   string `json:"project,omitempty"`
	LastLogin int64  `json:"last_login,omitempty"` // unix seconds; 0 = never
	Online    bool   `json:"online"`
}

// LocalSource provides a node's own presence and finger data to the ASSP
// server. Implemented by the host wiring (which has the presence hub + DB),
// keeping this package free of import cycles.
type LocalSource interface {
	Who() []PresenceEntry
	Finger(username string) (FingerResponse, bool)
}
