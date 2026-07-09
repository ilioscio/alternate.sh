package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Federated news storage (DESIGN.md §8.4). Articles carry an origin identity
// (origin_node + origin_id, the id on the node that authored them); local
// articles have a NULL origin. Parent references travel qualified the same
// way, so reply threading survives the hop; an unresolvable parent degrades
// to a thread root.

var (
	ErrNoGroup   = errors.New("no such newsgroup here")
	ErrModerated = errors.New("group is moderated; remote posts not accepted")
)

// FedArticle is an article in wire terms: origin ids, a qualified parent
// reference, and the origin node's own timestamps.
type FedArticle struct {
	OriginID         string
	Group            string
	Author           string // unqualified username on the origin node
	Subject          string
	Body             string
	Cancelled        bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
	ParentOriginNode string // both empty = root post
	ParentOriginID   string
}

// LocalArticlesSince returns locally-authored articles updated after since,
// oldest first, capped at limit — the answer to a peer's NEWS_SINCE.
// selfNode qualifies parent references and excludes this node's local
// namespace (<selfNode>.*) from federation.
func LocalArticlesSince(ctx context.Context, pool *pgxpool.Pool, selfNode string, since time.Time, limit int) ([]FedArticle, error) {
	rows, err := pool.Query(ctx, `
		SELECT a.id, ng.name, u.username, a.subject, a.body, a.cancelled,
		       a.created_at, a.updated_at,
		       COALESCE(p.origin_node, $1), COALESCE(p.origin_id, p.id)
		FROM articles a
		JOIN newsgroups ng ON ng.id = a.newsgroup_id
		JOIN users u ON u.id = a.author_id
		LEFT JOIN articles p ON p.id = a.parent_id
		WHERE a.origin_node IS NULL
		  AND a.updated_at > $2
		  AND ng.name NOT LIKE $1 || '.%'
		ORDER BY a.updated_at
		LIMIT $3`, selfNode, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FedArticle
	for rows.Next() {
		var a FedArticle
		var pNode, pID *string
		if err := rows.Scan(&a.OriginID, &a.Group, &a.Author, &a.Subject, &a.Body,
			&a.Cancelled, &a.CreatedAt, &a.UpdatedAt, &pNode, &pID); err != nil {
			return nil, err
		}
		if pNode != nil && pID != nil {
			a.ParentOriginNode, a.ParentOriginID = *pNode, *pID
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpsertFederatedArticle stores an article received from peerNode, or — when
// it already exists (push and catch-up sync overlap) — updates its cancelled
// flag. The group must exist locally and accept remote posts.
func UpsertFederatedArticle(ctx context.Context, pool *pgxpool.Pool, selfNode, peerNode string, a FedArticle) error {
	var groupID string
	var moderated bool
	err := pool.QueryRow(ctx,
		`SELECT id, moderated FROM newsgroups WHERE name = $1`, a.Group,
	).Scan(&groupID, &moderated)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNoGroup
	}
	if err != nil {
		return err
	}
	if moderated {
		return ErrModerated
	}

	// Resolve the qualified parent reference to a local row, if we have it.
	var parentID *string
	if a.ParentOriginID != "" {
		var id string
		var perr error
		if a.ParentOriginNode == selfNode {
			perr = pool.QueryRow(ctx,
				`SELECT id FROM articles WHERE id = $1`, a.ParentOriginID).Scan(&id)
		} else {
			perr = pool.QueryRow(ctx,
				`SELECT id FROM articles WHERE origin_node = $1 AND origin_id = $2`,
				a.ParentOriginNode, a.ParentOriginID).Scan(&id)
		}
		if perr == nil {
			parentID = &id
		} // unresolved parent → thread root
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO articles (newsgroup_id, remote_author, subject, body, parent_id,
		                      cancelled, created_at, origin_node, origin_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (origin_node, origin_id) WHERE origin_node IS NOT NULL
		DO UPDATE SET cancelled = EXCLUDED.cancelled, updated_at = NOW()`,
		groupID, a.Author+"@"+peerNode, a.Subject, a.Body, parentID,
		a.Cancelled, a.CreatedAt, peerNode, a.OriginID)
	return err
}

// CancelFederatedArticle cancels an article on behalf of its origin node.
// Scoping the match to (origin_node = peer) is what makes remote cancels
// origin-only: a peer can never cancel anyone else's articles here.
func CancelFederatedArticle(ctx context.Context, pool *pgxpool.Pool, peerNode, originID string) error {
	_, err := pool.Exec(ctx, `
		UPDATE articles SET cancelled = true, updated_at = NOW()
		WHERE origin_node = $1 AND origin_id = $2`, peerNode, originID)
	return err
}

// GetNewsSyncMark returns the catch-up high-water mark for a peer — the
// peer's own updated_at clock, never compared against ours.
func GetNewsSyncMark(ctx context.Context, pool *pgxpool.Pool, peerNode string) (time.Time, error) {
	var t time.Time
	err := pool.QueryRow(ctx,
		`SELECT last_synced_at FROM news_sync_state WHERE peer_node = $1`, peerNode).Scan(&t)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, nil
	}
	return t, err
}

// SetNewsSyncMark advances a peer's high-water mark (never backwards).
func SetNewsSyncMark(ctx context.Context, pool *pgxpool.Pool, peerNode string, t time.Time) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO news_sync_state (peer_node, last_synced_at) VALUES ($1, $2)
		ON CONFLICT (peer_node) DO UPDATE
		SET last_synced_at = GREATEST(news_sync_state.last_synced_at, EXCLUDED.last_synced_at)`,
		peerNode, t)
	return err
}
