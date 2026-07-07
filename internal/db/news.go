package db

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Newsgroup struct {
	ID          string
	Name        string
	Description string
	Moderated   bool
	Total       int
	Unread      int
}

type Article struct {
	ID          string
	NewsgroupID string
	GroupName   string
	AuthorID    string
	AuthorName  string
	Subject     string
	Body        string
	ParentID    *string
	Cancelled   bool
	Read        bool
	Depth       int
	CreatedAt   time.Time
}

func GetNewsgroups(ctx context.Context, pool *pgxpool.Pool, userID string) ([]Newsgroup, error) {
	rows, err := pool.Query(ctx, `
		SELECT ng.id, ng.name, ng.description, ng.moderated,
		       COUNT(a.id) FILTER (WHERE NOT a.cancelled)                                        AS total,
		       COUNT(a.id) FILTER (WHERE NOT a.cancelled AND NOT EXISTS (
		           SELECT 1 FROM article_reads ar WHERE ar.article_id = a.id AND ar.user_id = $1
		       ))                                                                                AS unread
		FROM newsgroups ng
		LEFT JOIN articles a ON a.newsgroup_id = ng.id
		GROUP BY ng.id
		ORDER BY ng.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []Newsgroup
	for rows.Next() {
		var g Newsgroup
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.Moderated, &g.Total, &g.Unread); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

func GetNewsgroupByName(ctx context.Context, pool *pgxpool.Pool, name string) (*Newsgroup, error) {
	g := &Newsgroup{}
	err := pool.QueryRow(ctx,
		`SELECT id, name, description, moderated, 0, 0 FROM newsgroups WHERE name = $1`, name,
	).Scan(&g.ID, &g.Name, &g.Description, &g.Moderated, &g.Total, &g.Unread)
	if err != nil {
		return nil, ErrNotFound
	}
	return g, nil
}

// GetArticles returns all non-cancelled articles in a newsgroup in thread order
// (root articles by created_at, replies nested under their parents).
func GetArticles(ctx context.Context, pool *pgxpool.Pool, groupID, userID string) ([]Article, error) {
	rows, err := pool.Query(ctx, `
		WITH RECURSIVE thread AS (
		    SELECT id, newsgroup_id, author_id, subject, body, parent_id, cancelled, created_at,
		           0 AS depth, id AS thread_root, created_at AS root_ts
		    FROM articles
		    WHERE newsgroup_id = $1 AND parent_id IS NULL AND NOT cancelled

		    UNION ALL

		    SELECT a.id, a.newsgroup_id, a.author_id, a.subject, a.body, a.parent_id, a.cancelled, a.created_at,
		           t.depth + 1, t.thread_root, t.root_ts
		    FROM articles a
		    JOIN thread t ON t.id = a.parent_id
		    WHERE NOT a.cancelled
		)
		SELECT t.id, t.newsgroup_id, ng.name, t.author_id, u.username,
		       t.subject, t.body, t.parent_id, t.cancelled, t.created_at, t.depth,
		       EXISTS(SELECT 1 FROM article_reads ar WHERE ar.article_id = t.id AND ar.user_id = $2) AS read
		FROM thread t
		JOIN newsgroups ng ON ng.id = t.newsgroup_id
		JOIN users u ON u.id = t.author_id
		ORDER BY t.root_ts, t.thread_root, t.depth, t.created_at`,
		groupID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var arts []Article
	for rows.Next() {
		var a Article
		if err := rows.Scan(&a.ID, &a.NewsgroupID, &a.GroupName, &a.AuthorID, &a.AuthorName,
			&a.Subject, &a.Body, &a.ParentID, &a.Cancelled, &a.CreatedAt, &a.Depth, &a.Read); err != nil {
			return nil, err
		}
		arts = append(arts, a)
	}
	return arts, rows.Err()
}

func GetArticle(ctx context.Context, pool *pgxpool.Pool, articleID string) (*Article, error) {
	a := &Article{}
	err := pool.QueryRow(ctx, `
		SELECT a.id, a.newsgroup_id, ng.name, a.author_id, u.username,
		       a.subject, a.body, a.parent_id, a.cancelled, a.created_at, 0, false
		FROM articles a
		JOIN newsgroups ng ON ng.id = a.newsgroup_id
		JOIN users u ON u.id = a.author_id
		WHERE a.id = $1`, articleID,
	).Scan(&a.ID, &a.NewsgroupID, &a.GroupName, &a.AuthorID, &a.AuthorName,
		&a.Subject, &a.Body, &a.ParentID, &a.Cancelled, &a.CreatedAt, &a.Depth, &a.Read)
	if err != nil {
		return nil, err
	}
	return a, nil
}

// CountArticlesPostedSince counts articles an author has posted within the
// given interval (e.g. '24 hours'), for spam rate limiting.
func CountArticlesPostedSince(ctx context.Context, pool *pgxpool.Pool, authorID, interval string) (int, error) {
	var n int
	err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM articles WHERE author_id = $1 AND created_at > NOW() - $2::interval`,
		authorID, interval,
	).Scan(&n)
	return n, err
}

func PostArticle(ctx context.Context, pool *pgxpool.Pool, groupID, authorID, subject, body string, parentID *string) (*Article, error) {
	a := &Article{}
	err := pool.QueryRow(ctx, `
		INSERT INTO articles (newsgroup_id, author_id, subject, body, parent_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, newsgroup_id, author_id, subject, body, parent_id, cancelled, created_at`,
		groupID, authorID, subject, body, parentID,
	).Scan(&a.ID, &a.NewsgroupID, &a.AuthorID, &a.Subject, &a.Body, &a.ParentID, &a.Cancelled, &a.CreatedAt)
	return a, err
}

func CancelArticle(ctx context.Context, pool *pgxpool.Pool, articleID, authorID string, isAdmin bool) (bool, error) {
	var tag string
	var err error
	if isAdmin {
		res, e := pool.Exec(ctx, `UPDATE articles SET cancelled = true WHERE id = $1`, articleID)
		err = e
		tag = res.String()
	} else {
		res, e := pool.Exec(ctx, `UPDATE articles SET cancelled = true WHERE id = $1 AND author_id = $2`, articleID, authorID)
		err = e
		tag = res.String()
	}
	if err != nil {
		return false, err
	}
	return tag != "UPDATE 0", nil
}

func MarkArticleRead(ctx context.Context, pool *pgxpool.Pool, articleID, userID string) error {
	_, err := pool.Exec(ctx,
		`INSERT INTO article_reads (user_id, article_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		userID, articleID)
	return err
}

func MarkGroupRead(ctx context.Context, pool *pgxpool.Pool, groupID, userID string) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO article_reads (user_id, article_id)
		SELECT $1, id FROM articles WHERE newsgroup_id = $2 AND NOT cancelled
		ON CONFLICT DO NOTHING`,
		userID, groupID)
	return err
}

func CountUnreadNews(ctx context.Context, pool *pgxpool.Pool, userID string) (int, error) {
	var n int
	err := pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM articles a
		WHERE NOT a.cancelled
		  AND NOT EXISTS (SELECT 1 FROM article_reads ar WHERE ar.article_id = a.id AND ar.user_id = $1)`,
		userID).Scan(&n)
	return n, err
}
