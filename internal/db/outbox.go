package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The mail outbox holds cross-node messages awaiting delivery (DESIGN.md
// §8.4): the delivery worker attempts each due entry, rescheduling with
// backoff on transient failure and bouncing to the sender's inbox when a
// peer rejects permanently or the message ages out.

type OutboxMail struct {
	ID         string
	SenderID   string
	SenderName string // username, joined for the wire's From field
	PeerNode   string
	RemoteUser string
	Subject    string
	Body       string
	CreatedAt  time.Time
	Attempts   int
	LastError  string
}

// EnqueueOutboxMail queues a message for cross-node delivery.
func EnqueueOutboxMail(ctx context.Context, pool *pgxpool.Pool, senderID, peerNode, remoteUser, subject, body string) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO mail_outbox (sender_id, peer_node, remote_user, subject, body)
		VALUES ($1, $2, $3, $4, $5)`,
		senderID, peerNode, remoteUser, subject, body)
	return err
}

// CountOutboxQueuedSince counts messages a user queued within the interval,
// so the hourly send limit covers remote mail that hasn't left yet.
func CountOutboxQueuedSince(ctx context.Context, pool *pgxpool.Pool, senderID, interval string) (int, error) {
	var n int
	err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM mail_outbox WHERE sender_id = $1 AND created_at > NOW() - $2::interval`,
		senderID, interval,
	).Scan(&n)
	return n, err
}

// DueOutboxMail returns messages whose next attempt is due, oldest first.
func DueOutboxMail(ctx context.Context, pool *pgxpool.Pool, limit int) ([]OutboxMail, error) {
	rows, err := pool.Query(ctx, `
		SELECT o.id, o.sender_id, u.username, o.peer_node, o.remote_user,
		       o.subject, o.body, o.created_at, o.attempts, o.last_error
		FROM mail_outbox o
		JOIN users u ON u.id = o.sender_id
		WHERE o.next_attempt <= NOW()
		ORDER BY o.next_attempt
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []OutboxMail
	for rows.Next() {
		var m OutboxMail
		if err := rows.Scan(&m.ID, &m.SenderID, &m.SenderName, &m.PeerNode, &m.RemoteUser,
			&m.Subject, &m.Body, &m.CreatedAt, &m.Attempts, &m.LastError); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// RescheduleOutboxMail records a failed attempt and sets the next one.
func RescheduleOutboxMail(ctx context.Context, pool *pgxpool.Pool, id, lastError string, next time.Time) error {
	_, err := pool.Exec(ctx, `
		UPDATE mail_outbox SET attempts = attempts + 1, last_error = $2, next_attempt = $3
		WHERE id = $1`, id, lastError, next)
	return err
}

// DeleteOutboxMail removes a delivered (or bounced) message from the queue.
func DeleteOutboxMail(ctx context.Context, pool *pgxpool.Pool, id string) error {
	_, err := pool.Exec(ctx, `DELETE FROM mail_outbox WHERE id = $1`, id)
	return err
}
