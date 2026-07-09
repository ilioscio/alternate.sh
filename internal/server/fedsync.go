package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilioscio/alternate.sh/internal/config"
	"github.com/ilioscio/alternate.sh/internal/db"
	"github.com/ilioscio/alternate.sh/internal/federation"
	"github.com/ilioscio/alternate.sh/internal/presence"
	"github.com/ilioscio/alternate.sh/internal/ratelimit"
)

// FedSync runs the store-and-forward side of federated mail & news
// (DESIGN.md §8.4): the mail outbox worker (attempt → backoff → bounce),
// push-on-post for news, and the periodic catch-up sync. It also implements
// the inbound handlers the ASSP server dispatches to, so receive-side rate
// limiting shares one home.
type FedSync struct {
	ctx  context.Context
	cfg  *config.Config
	pool *pgxpool.Pool
	hub  *presence.Hub

	kick chan struct{} // wakes the outbox worker for an immediate attempt

	// Receive-side limits: peering is trust, not a blank check.
	mailPerSender *ratelimit.Limiter
	mailPerPeer   *ratelimit.Limiter
	newsPerPeer   *ratelimit.Limiter
}

// Inbound size caps, mirroring the shell's own input limits.
const (
	maxFedSubject = 512
	maxFedBody    = 80 * 1024
)

// Outbox policy.
const (
	outboxScanEvery = 30 * time.Second
	newsSyncEvery   = time.Hour
	maxQueueAge     = 24 * time.Hour
	dialTimeout     = 20 * time.Second
)

// outboxBackoff is the retry ladder; attempts beyond it reuse the last rung.
var outboxBackoff = []time.Duration{
	time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, 4 * time.Hour,
}

func NewFedSync(ctx context.Context, cfg *config.Config, pool *pgxpool.Pool, hub *presence.Hub) *FedSync {
	return &FedSync{
		ctx:           ctx,
		cfg:           cfg,
		pool:          pool,
		hub:           hub,
		kick:          make(chan struct{}, 1),
		mailPerSender: ratelimit.New(60, time.Hour),
		mailPerPeer:   ratelimit.New(500, time.Hour),
		newsPerPeer:   ratelimit.New(600, time.Hour),
	}
}

func (f *FedSync) node() string { return f.cfg.Server.Hostname }

func fedPeer(p *db.Peer, defaultPort int) federation.Peer {
	return federation.Peer{
		Node:    p.Node,
		Address: federation.ResolveAddress(p.Address, p.Node, defaultPort),
		Secret:  p.Secret,
	}
}

// Run drives the workers until the context ends. Call in a goroutine.
func (f *FedSync) Run() {
	// Startup catch-up: pick up whatever happened while we were down.
	f.SyncNews()
	f.processOutbox()

	outbox := time.NewTicker(outboxScanEvery)
	news := time.NewTicker(newsSyncEvery)
	defer outbox.Stop()
	defer news.Stop()

	for {
		select {
		case <-f.ctx.Done():
			return
		case <-f.kick:
			f.processOutbox()
		case <-outbox.C:
			f.processOutbox()
		case <-news.C:
			f.SyncNews()
		}
	}
}

// ── shell.FederationNotifier ─────────────────────────────────────────────────

// MailQueued nudges the outbox worker so the normal case delivers instantly.
func (f *FedSync) MailQueued() {
	select {
	case f.kick <- struct{}{}:
	default:
	}
}

// ArticlePosted pushes a freshly posted local article to every peer.
func (f *FedSync) ArticlePosted(articleID string) {
	go f.pushArticle(articleID)
}

// PeerAdded reacts to `node add`: a peer just became (or returned to being)
// reachable, so pull its news immediately and retry any queued mail rather
// than waiting for the next tick.
func (f *FedSync) PeerAdded() {
	go f.SyncNews()
	f.MailQueued()
}

// ArticleCancelled pushes a cancel of a local article to every peer.
func (f *FedSync) ArticleCancelled(articleID string) {
	go f.forEachPeer(func(peer federation.Peer) {
		ctx, cancel := context.WithTimeout(f.ctx, dialTimeout)
		defer cancel()
		federation.PushCancel(ctx, f.node(), peer, articleID) // best effort; sync catches up
	})
}

// ── Outbox worker ─────────────────────────────────────────────────────────────

func (f *FedSync) processOutbox() {
	msgs, err := db.DueOutboxMail(f.ctx, f.pool, 20)
	if err != nil {
		return
	}
	for _, m := range msgs {
		f.attemptDelivery(m)
	}
}

func (f *FedSync) attemptDelivery(m db.OutboxMail) {
	peer, err := db.GetPeer(f.ctx, f.pool, m.PeerNode)
	if err != nil {
		f.bounce(m, m.PeerNode+" is no longer a federation peer")
		return
	}

	ctx, cancel := context.WithTimeout(f.ctx, dialTimeout)
	resp, err := federation.SendRemoteMail(ctx, f.node(), fedPeer(peer, f.cfg.Federation.ASSPPort),
		federation.MailDeliverRequest{
			From:    m.SenderName,
			To:      m.RemoteUser,
			Subject: m.Subject,
			Body:    m.Body,
		})
	cancel()

	switch {
	case err != nil:
		f.transientFailure(m, "host unreachable")
	case !resp.OK && resp.Permanent:
		f.bounce(m, resp.Reason)
	case !resp.OK:
		f.transientFailure(m, resp.Reason)
	default:
		db.DeleteOutboxMail(f.ctx, f.pool, m.ID)
		// Vacation auto-reply rode back in the response: deliver it into
		// the sender's inbox on their behalf.
		if resp.Vacation != "" {
			remote := m.RemoteUser + "@" + m.PeerNode
			db.DeliverRemoteMail(f.ctx, f.pool, remote, m.SenderID,
				"Auto-reply: "+m.Subject, resp.Vacation)
			f.biff(m.SenderName, remote, "Auto-reply: "+m.Subject)
		}
	}
}

func (f *FedSync) transientFailure(m db.OutboxMail, reason string) {
	if time.Since(m.CreatedAt) > maxQueueAge {
		f.bounce(m, fmt.Sprintf("undeliverable for %s (%s)", maxQueueAge, reason))
		return
	}
	rung := m.Attempts
	if rung >= len(outboxBackoff) {
		rung = len(outboxBackoff) - 1
	}
	db.RescheduleOutboxMail(f.ctx, f.pool, m.ID, reason, time.Now().Add(outboxBackoff[rung]))
}

// bounce returns a message to its sender as mail from MAILER-DAEMON —
// the authentic fate of undeliverable mail.
func (f *FedSync) bounce(m db.OutboxMail, reason string) {
	daemon := "MAILER-DAEMON@" + f.node()
	body := fmt.Sprintf(
		"Your mail to %s@%s could not be delivered.\n\nReason: %s\n\n----- Original message -----\nSubject: %s\n\n%s",
		m.RemoteUser, m.PeerNode, reason, m.Subject, m.Body)
	db.DeliverRemoteMail(f.ctx, f.pool, daemon, m.SenderID, "Returned mail: "+m.Subject, body)
	f.biff(m.SenderName, daemon, "Returned mail: "+m.Subject)
	db.DeleteOutboxMail(f.ctx, f.pool, m.ID)
}

func (f *FedSync) biff(username, from, subject string) {
	f.hub.Send(username, presence.WriteNotice{
		Kind:    presence.NoticeBiff,
		From:    from,
		Message: subject,
	})
}

// ── News push & catch-up sync ─────────────────────────────────────────────────

func (f *FedSync) forEachPeer(fn func(federation.Peer)) {
	peers, err := db.ListPeers(f.ctx, f.pool)
	if err != nil {
		return
	}
	for i := range peers {
		fn(fedPeer(&peers[i], f.cfg.Federation.ASSPPort))
	}
}

// pushArticle offers one local article to every peer, fire-and-forget:
// a peer that misses it catches up on its next sync.
func (f *FedSync) pushArticle(articleID string) {
	art, err := db.GetArticle(f.ctx, f.pool, articleID)
	if err != nil || art.OriginNode != nil {
		return // unknown, or not ours to push
	}
	if !federation.GroupFederates(art.GroupName, f.node()) {
		return
	}

	wire := federation.WireArticle{
		OriginID:  art.ID,
		Group:     art.GroupName,
		Author:    art.AuthorName,
		Subject:   art.Subject,
		Body:      art.Body,
		Cancelled: art.Cancelled,
		CreatedAt: federation.Micros(art.CreatedAt),
		UpdatedAt: federation.Micros(art.CreatedAt),
	}
	if art.ParentID != nil {
		if parent, err := db.GetArticle(f.ctx, f.pool, *art.ParentID); err == nil {
			wire.ParentOriginNode, wire.ParentOriginID = f.node(), parent.ID
			if parent.OriginNode != nil {
				wire.ParentOriginNode, wire.ParentOriginID = *parent.OriginNode, *parent.OriginID
			}
		}
	}

	f.forEachPeer(func(peer federation.Peer) {
		if !federation.GroupFederates(wire.Group, peer.Node) {
			return
		}
		ctx, cancel := context.WithTimeout(f.ctx, dialTimeout)
		defer cancel()
		federation.PushArticle(ctx, f.node(), peer, wire)
	})
}

// SyncNews runs one catch-up round against every peer: pull everything each
// peer authored since our per-peer high-water mark. Exported so `node add`
// can trigger an immediate first sync.
func (f *FedSync) SyncNews() {
	f.forEachPeer(func(peer federation.Peer) {
		mark, err := db.GetNewsSyncMark(f.ctx, f.pool, peer.Node)
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(f.ctx, time.Minute)
		defer cancel()
		newMark, _ := federation.PullNewsSince(ctx, f.node(), peer, mark, func(a federation.WireArticle) error {
			f.applyArticle(peer.Node, a) // policy rejects skip, they don't abort
			return nil
		})
		if newMark.After(mark) {
			db.SetNewsSyncMark(f.ctx, f.pool, peer.Node, newMark)
		}
	})
}

// applyArticle stores one article from a peer, enforcing federation policy.
// Used by both the push handler and the catch-up sync.
func (f *FedSync) applyArticle(peerNode string, a federation.WireArticle) error {
	if !federation.GroupFederates(a.Group, f.node()) {
		return errors.New("that group is local to this node")
	}
	if !federation.GroupFederates(a.Group, peerNode) {
		return errors.New("that group is local to your node")
	}
	if a.Author == "" || len(a.Author) > 64 || strings.ContainsAny(a.Author, "@ \t\n") {
		return errors.New("invalid author")
	}
	if len(a.Subject) > maxFedSubject || len(a.Body) > maxFedBody {
		return errors.New("article too large")
	}
	return db.UpsertFederatedArticle(f.ctx, f.pool, f.node(), peerNode, db.FedArticle{
		OriginID:         a.OriginID,
		Group:            a.Group,
		Author:           a.Author,
		Subject:          a.Subject,
		Body:             a.Body,
		Cancelled:        a.Cancelled,
		CreatedAt:        federation.FromMicros(a.CreatedAt),
		UpdatedAt:        federation.FromMicros(a.UpdatedAt),
		ParentOriginNode: a.ParentOriginNode,
		ParentOriginID:   a.ParentOriginID,
	})
}

// ── Inbound handlers (wired to the ASSP server) ──────────────────────────────

func (f *FedSync) handleMailDeliver(peerNode string, req federation.MailDeliverRequest) federation.MailDeliverResponse {
	if req.From == "" || len(req.From) > 64 || strings.ContainsAny(req.From, "@ \t\n") {
		return federation.MailDeliverResponse{OK: false, Reason: "invalid sender", Permanent: true}
	}
	if len(req.Subject) > maxFedSubject || len(req.Body) > maxFedBody {
		return federation.MailDeliverResponse{OK: false, Reason: "message too large", Permanent: true}
	}
	fromAddr := req.From + "@" + peerNode
	if !f.mailPerPeer.Allow(peerNode) || !f.mailPerSender.Allow(fromAddr) {
		return federation.MailDeliverResponse{OK: false, Reason: "rate limited"}
	}

	u, err := db.GetUserByUsername(f.ctx, f.pool, req.To)
	if err != nil {
		return federation.MailDeliverResponse{OK: false, Reason: "no such user", Permanent: true}
	}
	if err := db.DeliverRemoteMail(f.ctx, f.pool, fromAddr, u.ID, req.Subject, req.Body); err != nil {
		return federation.MailDeliverResponse{OK: false, Reason: "storage error"}
	}
	f.biff(u.Username, fromAddr, req.Subject)

	resp := federation.MailDeliverResponse{OK: true}
	if u.Vacation && u.VacationMessage != "" {
		if ok, _ := db.ShouldSendVacationReplyRemote(f.ctx, f.pool, u.ID, fromAddr); ok {
			db.RecordVacationReplyRemote(f.ctx, f.pool, u.ID, fromAddr)
			resp.Vacation = u.VacationMessage
		}
	}
	return resp
}

func (f *FedSync) handleNewsArticle(peerNode string, art federation.WireArticle) federation.NewsArticleResponse {
	if !f.newsPerPeer.Allow(peerNode) {
		return federation.NewsArticleResponse{OK: false, Reason: "rate limited"}
	}
	if err := f.applyArticle(peerNode, art); err != nil {
		return federation.NewsArticleResponse{OK: false, Reason: err.Error()}
	}
	return federation.NewsArticleResponse{OK: true}
}

func (f *FedSync) handleNewsCancel(peerNode, originID string) {
	db.CancelFederatedArticle(f.ctx, f.pool, peerNode, originID)
}

func (f *FedSync) handleNewsSince(peerNode string, since int64) federation.NewsSinceResponse {
	arts, err := db.LocalArticlesSince(f.ctx, f.pool, f.node(),
		federation.FromMicros(since), federation.NewsBatchLimit)
	if err != nil {
		return federation.NewsSinceResponse{}
	}
	resp := federation.NewsSinceResponse{More: len(arts) == federation.NewsBatchLimit}
	for _, a := range arts {
		resp.Articles = append(resp.Articles, federation.WireArticle{
			OriginID:         a.OriginID,
			Group:            a.Group,
			Author:           a.Author,
			Subject:          a.Subject,
			Body:             a.Body,
			Cancelled:        a.Cancelled,
			CreatedAt:        federation.Micros(a.CreatedAt),
			UpdatedAt:        federation.Micros(a.UpdatedAt),
			ParentOriginNode: a.ParentOriginNode,
			ParentOriginID:   a.ParentOriginID,
		})
	}
	return resp
}
