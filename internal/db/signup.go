package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrUsernameTaken and ErrEmailTaken distinguish signup conflicts.
var (
	ErrUsernameTaken = errors.New("username taken")
	ErrEmailTaken    = errors.New("email already registered")
)

type PendingAccount struct {
	ID           string
	Username     string
	Email        string
	PasswordHash string
	Token        string
	Code         string
	Attempts     int
}

// CreatePendingAccount stores an unconfirmed signup. It fails with
// ErrUsernameTaken if the username is already a confirmed user or an existing
// pending signup, or ErrEmailTaken if the email belongs to a confirmed user.
// Returns the token and code to send to the applicant.
func CreatePendingAccount(ctx context.Context, pool *pgxpool.Pool, username, email, passwordHash, code, fromIP string) (token string, err error) {
	// Confirmed-user conflicts.
	var exists bool
	if err = pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE lower(username) = lower($1))`, username,
	).Scan(&exists); err != nil {
		return "", err
	}
	if exists {
		return "", ErrUsernameTaken
	}
	if err = pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE email <> '' AND lower(email) = lower($1))`, email,
	).Scan(&exists); err != nil {
		return "", err
	}
	if exists {
		return "", ErrEmailTaken
	}

	// Replace any prior pending signup for the same username (case-insensitive),
	// so a re-submit refreshes the token/code instead of hitting the unique index.
	if _, err = pool.Exec(ctx,
		`DELETE FROM pending_accounts WHERE lower(username) = lower($1)`, username,
	); err != nil {
		return "", err
	}

	err = pool.QueryRow(ctx, `
		INSERT INTO pending_accounts (username, email, password_hash, code, from_ip)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING token::text`,
		username, email, passwordHash, code, fromIP,
	).Scan(&token)
	return token, err
}

// GetPendingByUsername returns a non-expired pending account by username.
func GetPendingByUsername(ctx context.Context, pool *pgxpool.Pool, username string) (*PendingAccount, error) {
	return scanPending(pool.QueryRow(ctx, `
		SELECT id::text, username, email, password_hash, token::text, code, attempts
		FROM pending_accounts
		WHERE lower(username) = lower($1) AND expires_at > NOW()`, username))
}

func scanPending(row pgx.Row) (*PendingAccount, error) {
	p := &PendingAccount{}
	err := row.Scan(&p.ID, &p.Username, &p.Email, &p.PasswordHash, &p.Token, &p.Code, &p.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// IncrementPendingAttempts bumps the guess counter and returns the new value.
func IncrementPendingAttempts(ctx context.Context, pool *pgxpool.Pool, id string) (int, error) {
	var n int
	err := pool.QueryRow(ctx,
		`UPDATE pending_accounts SET attempts = attempts + 1 WHERE id = $1 RETURNING attempts`, id,
	).Scan(&n)
	return n, err
}

// DeletePending removes a pending signup (e.g. after too many failed codes).
func DeletePending(ctx context.Context, pool *pgxpool.Pool, id string) error {
	_, err := pool.Exec(ctx, `DELETE FROM pending_accounts WHERE id = $1`, id)
	return err
}

// ConfirmPendingAccount promotes a pending signup to a real user in one
// transaction: it creates the users row (carrying the email) and deletes the
// pending row. Returns the new user. Fails with ErrUsernameTaken/ErrEmailTaken
// if someone claimed the identity between signup and confirmation.
func ConfirmPendingAccount(ctx context.Context, pool *pgxpool.Pool, p *PendingAccount) (*User, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var exists bool
	if err = tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE lower(username) = lower($1))`, p.Username,
	).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrUsernameTaken
	}
	if err = tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE email <> '' AND lower(email) = lower($1))`, p.Email,
	).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return nil, ErrEmailTaken
	}

	row := tx.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, email)
		VALUES ($1, $2, $3)
		RETURNING`+userColumns,
		p.Username, p.PasswordHash, p.Email,
	)
	u, err := scanUser(row)
	if err != nil {
		return nil, err
	}

	if _, err = tx.Exec(ctx, `DELETE FROM pending_accounts WHERE id = $1`, p.ID); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return u, nil
}

// CountRecentSignupsFromIP counts pending signups created by an IP within the
// given interval (e.g. '1 hour'), for rate limiting at the DB layer.
func CountRecentSignupsFromIP(ctx context.Context, pool *pgxpool.Pool, ip, interval string) (int, error) {
	var n int
	err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM pending_accounts WHERE from_ip = $1 AND created_at > NOW() - $2::interval`,
		ip, interval,
	).Scan(&n)
	return n, err
}

// CleanupExpiredPending deletes expired pending signups (run by the janitor).
func CleanupExpiredPending(ctx context.Context, pool *pgxpool.Pool) {
	pool.Exec(ctx, `DELETE FROM pending_accounts WHERE expires_at < NOW()`)
}
