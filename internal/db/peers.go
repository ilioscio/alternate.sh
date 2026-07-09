package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Peer struct {
	ID      string
	Node    string
	Address string
	Secret  string
	AddedAt time.Time
}

// AddPeer inserts or updates a federation peer (keyed by node name). Re-adding
// an existing node refreshes its address and secret.
func AddPeer(ctx context.Context, pool *pgxpool.Pool, node, address, secret string) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO peers (node, address, secret)
		VALUES ($1, $2, $3)
		ON CONFLICT (node) DO UPDATE SET address = EXCLUDED.address, secret = EXCLUDED.secret`,
		node, address, secret,
	)
	return err
}

// RemovePeer deletes a peer by node name, reporting whether a row was removed.
func RemovePeer(ctx context.Context, pool *pgxpool.Pool, node string) (bool, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM peers WHERE node = $1`, node)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// ListPeers returns all peers ordered by node name (secrets included; callers
// must not display them).
func ListPeers(ctx context.Context, pool *pgxpool.Pool) ([]Peer, error) {
	rows, err := pool.Query(ctx,
		`SELECT id::text, node, address, secret, added_at FROM peers ORDER BY node`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var peers []Peer
	for rows.Next() {
		var p Peer
		if err := rows.Scan(&p.ID, &p.Node, &p.Address, &p.Secret, &p.AddedAt); err != nil {
			return nil, err
		}
		peers = append(peers, p)
	}
	return peers, rows.Err()
}

// GetPeer returns a peer by node name, or ErrNotFound.
func GetPeer(ctx context.Context, pool *pgxpool.Pool, node string) (*Peer, error) {
	var p Peer
	err := pool.QueryRow(ctx,
		`SELECT id::text, node, address, secret, added_at FROM peers WHERE node = $1`, node,
	).Scan(&p.ID, &p.Node, &p.Address, &p.Secret, &p.AddedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// GetPeerSecret returns the shared secret for a peer node, and whether it
// exists. Used as the ASSP handshake's SecretFunc.
func GetPeerSecret(ctx context.Context, pool *pgxpool.Pool, node string) (string, bool, error) {
	var secret string
	err := pool.QueryRow(ctx, `SELECT secret FROM peers WHERE node = $1`, node).Scan(&secret)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return secret, true, nil
}
