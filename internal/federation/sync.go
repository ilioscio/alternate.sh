package federation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ilioscio/alternate.sh/internal/assp"
)

// Mail delivery and news sync verbs (DESIGN.md §8.4). All are ordinary
// request/response on the control channel — no connection handoff, no
// streams. The sending node's identity always comes from the peering
// handshake, never from the payload.

const (
	VerbMailSend    = "MAIL_SEND"
	VerbNewsArticle = "NEWS_ARTICLE"
	VerbNewsCancel  = "NEWS_CANCEL"
	VerbNewsSince   = "NEWS_SINCE"
)

// NewsBatchLimit caps one NEWS_SINCE response; More signals another round.
const NewsBatchLimit = 200

// MailDeliverRequest asks the peer to deliver mail to one of its users.
type MailDeliverRequest struct {
	From    string `json:"from"` // sender's username on the origin node
	To      string `json:"to"`   // recipient's username on the receiving node
	Subject string `json:"subject"`
	Body    string `json:"body"`
}

// MailDeliverResponse reports the outcome. Permanent failures (no such
// user) bounce immediately; transient ones retry. Vacation carries the
// recipient's auto-reply when one is due, delivered into the sender's
// inbox by the sender's own node.
type MailDeliverResponse struct {
	OK        bool   `json:"ok"`
	Reason    string `json:"reason,omitempty"`
	Permanent bool   `json:"permanent,omitempty"`
	Vacation  string `json:"vacation,omitempty"`
}

// WireArticle is one federated article. Timestamps are unix microseconds on
// the origin node's clock — the receiver stores them for display and hands
// UpdatedAt back verbatim as its sync mark, so clocks are never compared
// across nodes.
type WireArticle struct {
	OriginID  string `json:"origin_id"`
	Group     string `json:"group"`
	Author    string `json:"author"` // unqualified; receiver qualifies with the peer's name
	Subject   string `json:"subject"`
	Body      string `json:"body"`
	Cancelled bool   `json:"cancelled,omitempty"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`

	// Qualified parent reference; both empty = root post.
	ParentOriginNode string `json:"parent_node,omitempty"`
	ParentOriginID   string `json:"parent_id,omitempty"`
}

// NewsArticleResponse acknowledges (or refuses) one pushed article.
type NewsArticleResponse struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
}

// NewsSinceRequest asks for the peer's own articles updated after Since
// (unix microseconds, the peer's clock — i.e. the mark it last gave us).
type NewsSinceRequest struct {
	Since int64 `json:"since"`
}

// NewsSinceResponse returns one batch, oldest first.
type NewsSinceResponse struct {
	Articles []WireArticle `json:"articles"`
	More     bool          `json:"more,omitempty"` // batch was full; ask again
}

// NewsCancelRequest retracts one of the sender's own articles.
type NewsCancelRequest struct {
	OriginID string `json:"origin_id"`
}

// GroupFederates reports whether a newsgroup federates with respect to a
// node: <node>.* groups are that node's local namespace and never leave it
// (§5.6, §8.4). Callers check against both the local and the peer node.
func GroupFederates(group, node string) bool {
	return !strings.HasPrefix(group, node+".")
}

// Micros converts to the wire's timestamp form.
func Micros(t time.Time) int64 { return t.UnixMicro() }

// FromMicros converts a wire timestamp back to time.
func FromMicros(us int64) time.Time { return time.UnixMicro(us) }

// requestData marshals a Data-carrying request and decodes its response.
func requestData(ac *assp.Conn, verb string, payload, out any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return request(ac, Request{Verb: verb, Data: data}, out)
}

// SendRemoteMail dials the peer and requests delivery of one message.
func SendRemoteMail(ctx context.Context, localNode string, peer Peer, req MailDeliverRequest) (MailDeliverResponse, error) {
	ac, err := dial(ctx, localNode, peer)
	if err != nil {
		return MailDeliverResponse{}, err
	}
	defer ac.Close()
	var resp MailDeliverResponse
	err = requestData(ac, VerbMailSend, req, &resp)
	return resp, err
}

// PushArticle dials the peer and offers one locally-authored article.
func PushArticle(ctx context.Context, localNode string, peer Peer, art WireArticle) (NewsArticleResponse, error) {
	ac, err := dial(ctx, localNode, peer)
	if err != nil {
		return NewsArticleResponse{}, err
	}
	defer ac.Close()
	var resp NewsArticleResponse
	err = requestData(ac, VerbNewsArticle, art, &resp)
	return resp, err
}

// PushCancel dials the peer and retracts one locally-authored article.
func PushCancel(ctx context.Context, localNode string, peer Peer, originID string) error {
	ac, err := dial(ctx, localNode, peer)
	if err != nil {
		return err
	}
	defer ac.Close()
	var resp NewsArticleResponse
	return requestData(ac, VerbNewsCancel, NewsCancelRequest{OriginID: originID}, &resp)
}

// PullNewsSince performs a full catch-up sync against one peer over a single
// connection: batches from `since` until the peer has no more, applying each
// article in arrival order. It returns the new high-water mark (the peer's
// clock). apply errors abort the sync with the mark advanced only through
// what was applied.
func PullNewsSince(ctx context.Context, localNode string, peer Peer, since time.Time, apply func(WireArticle) error) (time.Time, error) {
	ac, err := dial(ctx, localNode, peer)
	if err != nil {
		return since, err
	}
	defer ac.Close()

	mark := since
	for {
		var resp NewsSinceResponse
		if err := requestData(ac, VerbNewsSince, NewsSinceRequest{Since: Micros(mark)}, &resp); err != nil {
			return mark, err
		}
		for _, art := range resp.Articles {
			if err := apply(art); err != nil {
				return mark, fmt.Errorf("applying article %s: %w", art.OriginID, err)
			}
			if t := FromMicros(art.UpdatedAt); t.After(mark) {
				mark = t
			}
		}
		if !resp.More {
			return mark, nil
		}
	}
}
