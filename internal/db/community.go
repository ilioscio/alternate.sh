package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Phase 7.1 storage: fortune review queue, mailing lists, moderated-group
// approval queue, bans, and the admin audit log.

// ── Fortunes ─────────────────────────────────────────────────────────────────

type Fortune struct {
	ID        string
	Body      string
	Submitter string // username, or "" if the account is gone
	CreatedAt time.Time
}

// SubmitFortune queues a fortune for review.
func SubmitFortune(ctx context.Context, pool *pgxpool.Pool, userID, body string) error {
	_, err := pool.Exec(ctx,
		`INSERT INTO fortunes (body, added_by, status) VALUES ($1, $2, 'pending')`, body, userID)
	return err
}

// CountFortunesSubmittedSince rate-limits submissions per user.
func CountFortunesSubmittedSince(ctx context.Context, pool *pgxpool.Pool, userID, interval string) (int, error) {
	var n int
	err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM fortunes WHERE added_by = $1 AND created_at > NOW() - $2::interval`,
		userID, interval).Scan(&n)
	return n, err
}

// PendingFortunes lists the review queue, oldest first.
func PendingFortunes(ctx context.Context, pool *pgxpool.Pool) ([]Fortune, error) {
	rows, err := pool.Query(ctx, `
		SELECT f.id, f.body, COALESCE(u.username, ''), f.created_at
		FROM fortunes f LEFT JOIN users u ON u.id = f.added_by
		WHERE f.status = 'pending' ORDER BY f.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Fortune
	for rows.Next() {
		var f Fortune
		if err := rows.Scan(&f.ID, &f.Body, &f.Submitter, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ReviewFortune approves or rejects a pending fortune, returning its
// submitter's username (for the notice) and whether a row changed.
func ReviewFortune(ctx context.Context, pool *pgxpool.Pool, id string, approve bool) (string, bool, error) {
	status := "rejected"
	if approve {
		status = "approved"
	}
	var submitter *string
	err := pool.QueryRow(ctx, `
		UPDATE fortunes f SET status = $2
		FROM users u
		WHERE f.id = $1 AND f.status = 'pending' AND u.id = f.added_by
		RETURNING u.username`, id, status).Scan(&submitter)
	if errors.Is(err, pgx.ErrNoRows) {
		// Either no such pending fortune, or the submitter's account is
		// gone — try again without the join.
		tag, err2 := pool.Exec(ctx,
			`UPDATE fortunes SET status = $2 WHERE id = $1 AND status = 'pending'`, id, status)
		if err2 != nil {
			return "", false, err2
		}
		return "", tag.RowsAffected() > 0, nil
	}
	if err != nil {
		return "", false, err
	}
	name := ""
	if submitter != nil {
		name = *submitter
	}
	return name, true, nil
}

// ── Mailing lists ────────────────────────────────────────────────────────────

type MailingList struct {
	ID            string
	Name          string
	Description   string
	AdminOnlyPost bool
	Members       int
	Subscribed    bool
}

// CreateMailingList adds a list; the caller has already checked the name
// doesn't collide with a username.
func CreateMailingList(ctx context.Context, pool *pgxpool.Pool, name, description string) error {
	_, err := pool.Exec(ctx,
		`INSERT INTO mailing_lists (name, description) VALUES ($1, $2)`, name, description)
	return err
}

func DeleteMailingList(ctx context.Context, pool *pgxpool.Pool, name string) (bool, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM mailing_lists WHERE name = $1`, name)
	return tag.RowsAffected() > 0, err
}

func SetMailingListAdminOnly(ctx context.Context, pool *pgxpool.Pool, name string, adminOnly bool) (bool, error) {
	tag, err := pool.Exec(ctx,
		`UPDATE mailing_lists SET admin_only_post = $2 WHERE name = $1`, name, adminOnly)
	return tag.RowsAffected() > 0, err
}

// GetMailingList returns a list by name, with the viewer's membership.
func GetMailingList(ctx context.Context, pool *pgxpool.Pool, name, viewerID string) (*MailingList, error) {
	l := &MailingList{}
	err := pool.QueryRow(ctx, `
		SELECT l.id, l.name, l.description, l.admin_only_post,
		       (SELECT COUNT(*) FROM list_members m WHERE m.list_id = l.id),
		       EXISTS(SELECT 1 FROM list_members m WHERE m.list_id = l.id AND m.user_id = $2)
		FROM mailing_lists l WHERE l.name = $1`, name, viewerID,
	).Scan(&l.ID, &l.Name, &l.Description, &l.AdminOnlyPost, &l.Members, &l.Subscribed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return l, err
}

// ListMailingLists returns every list with the viewer's membership marked.
func ListMailingLists(ctx context.Context, pool *pgxpool.Pool, viewerID string) ([]MailingList, error) {
	rows, err := pool.Query(ctx, `
		SELECT l.id, l.name, l.description, l.admin_only_post,
		       (SELECT COUNT(*) FROM list_members m WHERE m.list_id = l.id),
		       EXISTS(SELECT 1 FROM list_members m WHERE m.list_id = l.id AND m.user_id = $1)
		FROM mailing_lists l ORDER BY l.name`, viewerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MailingList
	for rows.Next() {
		var l MailingList
		if err := rows.Scan(&l.ID, &l.Name, &l.Description, &l.AdminOnlyPost, &l.Members, &l.Subscribed); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Subscribe adds a member (idempotent); reports whether they were new.
func Subscribe(ctx context.Context, pool *pgxpool.Pool, listID, userID string) (bool, error) {
	tag, err := pool.Exec(ctx, `
		INSERT INTO list_members (list_id, user_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, listID, userID)
	return tag.RowsAffected() > 0, err
}

func Unsubscribe(ctx context.Context, pool *pgxpool.Pool, listID, userID string) (bool, error) {
	tag, err := pool.Exec(ctx,
		`DELETE FROM list_members WHERE list_id = $1 AND user_id = $2`, listID, userID)
	return tag.RowsAffected() > 0, err
}

// ListMemberIDs returns subscriber user ids and usernames for fan-out.
func ListMemberIDs(ctx context.Context, pool *pgxpool.Pool, listID string) (ids []string, usernames []string, err error) {
	rows, err := pool.Query(ctx, `
		SELECT u.id, u.username FROM list_members m JOIN users u ON u.id = m.user_id
		WHERE m.list_id = $1`, listID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, nil, err
		}
		ids = append(ids, id)
		usernames = append(usernames, name)
	}
	return ids, usernames, rows.Err()
}

// ── Moderation queue ─────────────────────────────────────────────────────────

// PendingArticles lists unapproved articles across all groups, oldest first.
func PendingArticles(ctx context.Context, pool *pgxpool.Pool) ([]Article, error) {
	rows, err := pool.Query(ctx, `
		SELECT a.id, a.newsgroup_id, ng.name, a.author_id, COALESCE(u.username, a.remote_author, '?'),
		       a.subject, a.body, a.parent_id, a.cancelled, a.created_at, 0, false,
		       a.origin_node, a.origin_id
		FROM articles a
		JOIN newsgroups ng ON ng.id = a.newsgroup_id
		LEFT JOIN users u ON u.id = a.author_id
		WHERE NOT a.approved AND NOT a.cancelled
		ORDER BY a.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Article
	for rows.Next() {
		var a Article
		if err := rows.Scan(&a.ID, &a.NewsgroupID, &a.GroupName, &a.AuthorID, &a.AuthorName,
			&a.Subject, &a.Body, &a.ParentID, &a.Cancelled, &a.CreatedAt, &a.Depth, &a.Read,
			&a.OriginNode, &a.OriginID); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ReviewArticle approves (making it live and federable — updated_at bumps so
// catch-up sync picks it up) or rejects (cancels) a pending article.
func ReviewArticle(ctx context.Context, pool *pgxpool.Pool, id string, approve bool) (bool, error) {
	var tag string
	if approve {
		res, err := pool.Exec(ctx, `
			UPDATE articles SET approved = true, updated_at = NOW()
			WHERE id = $1 AND NOT approved`, id)
		if err != nil {
			return false, err
		}
		tag = res.String()
	} else {
		res, err := pool.Exec(ctx, `
			UPDATE articles SET cancelled = true, updated_at = NOW()
			WHERE id = $1 AND NOT approved`, id)
		if err != nil {
			return false, err
		}
		tag = res.String()
	}
	return tag != "UPDATE 0", nil
}

// ── Bans ─────────────────────────────────────────────────────────────────────

// SetBanned bans or unbans a user; reports whether the user existed.
func SetBanned(ctx context.Context, pool *pgxpool.Pool, username string, banned bool, reason string) (bool, error) {
	tag, err := pool.Exec(ctx,
		`UPDATE users SET banned = $2, ban_reason = $3 WHERE username = $1`,
		username, banned, reason)
	return tag.RowsAffected() > 0, err
}

// ── Audit log ────────────────────────────────────────────────────────────────

type AuditEntry struct {
	Actor     string
	Action    string
	Target    string
	Detail    string
	CreatedAt time.Time
}

// RecordAudit logs one admin action. Failures are swallowed by callers —
// auditing must never block moderation itself.
func RecordAudit(ctx context.Context, pool *pgxpool.Pool, actorID, action, target, detail string) error {
	_, err := pool.Exec(ctx,
		`INSERT INTO audit_log (actor_id, action, target, detail) VALUES ($1, $2, $3, $4)`,
		actorID, action, target, detail)
	return err
}

// ListAudit returns the most recent admin actions.
func ListAudit(ctx context.Context, pool *pgxpool.Pool, limit int) ([]AuditEntry, error) {
	rows, err := pool.Query(ctx, `
		SELECT COALESCE(u.username, '(gone)'), a.action, a.target, a.detail, a.created_at
		FROM audit_log a LEFT JOIN users u ON u.id = a.actor_id
		ORDER BY a.created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.Actor, &e.Action, &e.Target, &e.Detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
