package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MailMessage struct {
	ID          string
	SenderID    string
	SenderName  string
	RecipientID string
	Subject     string
	Body        string
	InReplyTo   *string
	ReadAt      *time.Time
	CreatedAt   time.Time
}

// CountMailSentSince counts messages a user has sent within the given
// interval (e.g. '1 hour'), for spam rate limiting.
func CountMailSentSince(ctx context.Context, pool *pgxpool.Pool, senderID, interval string) (int, error) {
	var n int
	err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM mail WHERE sender_id = $1 AND created_at > NOW() - $2::interval`,
		senderID, interval,
	).Scan(&n)
	return n, err
}

func SendMail(ctx context.Context, pool *pgxpool.Pool, senderID, recipientID, subject, body string, inReplyTo *string) (*MailMessage, error) {
	m := &MailMessage{}
	err := pool.QueryRow(ctx, `
		INSERT INTO mail (sender_id, recipient_id, subject, body, in_reply_to)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, sender_id, recipient_id, subject, body, in_reply_to, read_at, created_at`,
		senderID, recipientID, subject, body, inReplyTo,
	).Scan(&m.ID, &m.SenderID, &m.RecipientID, &m.Subject, &m.Body, &m.InReplyTo, &m.ReadAt, &m.CreatedAt)
	return m, err
}

func GetInbox(ctx context.Context, pool *pgxpool.Pool, userID string) ([]MailMessage, error) {
	rows, err := pool.Query(ctx, `
		SELECT m.id, m.sender_id, u.username, m.recipient_id,
		       m.subject, m.body, m.in_reply_to, m.read_at, m.created_at
		FROM mail m
		JOIN users u ON u.id = m.sender_id
		WHERE m.recipient_id = $1 AND NOT m.deleted_by_recipient
		ORDER BY m.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []MailMessage
	for rows.Next() {
		var m MailMessage
		if err := rows.Scan(&m.ID, &m.SenderID, &m.SenderName, &m.RecipientID,
			&m.Subject, &m.Body, &m.InReplyTo, &m.ReadAt, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

func CountUnreadMail(ctx context.Context, pool *pgxpool.Pool, userID string) (int, error) {
	var n int
	err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM mail WHERE recipient_id = $1 AND read_at IS NULL AND NOT deleted_by_recipient`,
		userID).Scan(&n)
	return n, err
}

func MarkMailRead(ctx context.Context, pool *pgxpool.Pool, mailID string) error {
	_, err := pool.Exec(ctx,
		`UPDATE mail SET read_at = NOW() WHERE id = $1 AND read_at IS NULL`, mailID)
	return err
}

func DeleteMailForRecipient(ctx context.Context, pool *pgxpool.Pool, mailID, recipientID string) error {
	_, err := pool.Exec(ctx,
		`UPDATE mail SET deleted_by_recipient = true WHERE id = $1 AND recipient_id = $2`,
		mailID, recipientID)
	return err
}

// ShouldSendVacationReply returns true if no auto-reply has been sent to senderID
// on behalf of vacationerID in the past 7 days.
func ShouldSendVacationReply(ctx context.Context, pool *pgxpool.Pool, vacationerID, senderID string) (bool, error) {
	var sentAt time.Time
	err := pool.QueryRow(ctx,
		`SELECT sent_at FROM vacation_replies WHERE vacationer_id = $1 AND sender_id = $2`,
		vacationerID, senderID).Scan(&sentAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return time.Since(sentAt) >= 7*24*time.Hour, nil
}

func RecordVacationReply(ctx context.Context, pool *pgxpool.Pool, vacationerID, senderID string) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO vacation_replies (vacationer_id, sender_id, sent_at) VALUES ($1, $2, NOW())
		ON CONFLICT (vacationer_id, sender_id) DO UPDATE SET sent_at = NOW()`,
		vacationerID, senderID)
	return err
}
