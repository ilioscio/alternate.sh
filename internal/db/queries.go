package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ilioscio/alternate.sh/internal/valid"
)

var ErrNotFound = errors.New("not found")

type User struct {
	ID              string
	Username        string
	DisplayName     string
	PasswordHash    string
	Office          string
	HomePhone       string
	Plan            string
	Project         string
	Signature       string
	PublicPage      string
	MesgOn          bool
	Vacation        bool
	VacationMessage string
	HushLogin       bool
	Admin           bool
	Calendar        string
	Biff            bool
	Email           string
	CreatedAt       time.Time
	LastLogin       *time.Time
	Banned          bool
	BanReason       string
}

type SSHKey struct {
	ID      string
	UserID  string
	KeyType string
	KeyData string
	Comment string
}

type LoginRecord struct {
	ID          string
	Username    string
	TTY         string
	FromAddr    string
	LoggedInAt  time.Time
	LoggedOutAt *time.Time
}

const userColumns = `
	id, username, display_name, password_hash, office, home_phone,
	plan, project, signature, public_page, mesg_on, vacation,
	vacation_message, hush_login, admin, calendar, biff, email, created_at, last_login,
	banned, ban_reason`

func scanUser(row pgx.Row) (*User, error) {
	u := &User{}
	err := row.Scan(
		&u.ID, &u.Username, &u.DisplayName, &u.PasswordHash,
		&u.Office, &u.HomePhone, &u.Plan, &u.Project,
		&u.Signature, &u.PublicPage, &u.MesgOn, &u.Vacation,
		&u.VacationMessage, &u.HushLogin, &u.Admin, &u.Calendar, &u.Biff, &u.Email, &u.CreatedAt, &u.LastLogin,
		&u.Banned, &u.BanReason,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return u, err
}

func GetUserByUsername(ctx context.Context, pool *pgxpool.Pool, username string) (*User, error) {
	row := pool.QueryRow(ctx, `SELECT`+userColumns+` FROM users WHERE username = $1`, username)
	return scanUser(row)
}

func GetUserByID(ctx context.Context, pool *pgxpool.Pool, id string) (*User, error) {
	row := pool.QueryRow(ctx, `SELECT`+userColumns+` FROM users WHERE id = $1`, id)
	return scanUser(row)
}

func CreateUser(ctx context.Context, pool *pgxpool.Pool, username, passwordHash, displayName string, admin bool) (*User, error) {
	if err := valid.ValidateUsername(username); err != nil {
		return nil, err
	}
	row := pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, display_name, admin)
		VALUES ($1, $2, $3, $4)
		RETURNING`+userColumns,
		username, passwordHash, displayName, admin,
	)
	return scanUser(row)
}

func UpdateLastLogin(ctx context.Context, pool *pgxpool.Pool, userID string) error {
	_, err := pool.Exec(ctx, `UPDATE users SET last_login = NOW() WHERE id = $1`, userID)
	return err
}

func UpdatePlan(ctx context.Context, pool *pgxpool.Pool, userID, plan string) error {
	_, err := pool.Exec(ctx, `UPDATE users SET plan = $1 WHERE id = $2`, plan, userID)
	return err
}

func UpdateProject(ctx context.Context, pool *pgxpool.Pool, userID, project string) error {
	_, err := pool.Exec(ctx, `UPDATE users SET project = $1 WHERE id = $2`, project, userID)
	return err
}

func UpdatePublicPage(ctx context.Context, pool *pgxpool.Pool, userID, page string) error {
	_, err := pool.Exec(ctx, `UPDATE users SET public_page = $1 WHERE id = $2`, page, userID)
	return err
}

func UpdateCalendar(ctx context.Context, pool *pgxpool.Pool, userID, calendar string) error {
	_, err := pool.Exec(ctx, `UPDATE users SET calendar = $1 WHERE id = $2`, calendar, userID)
	return err
}

func UpdatePassword(ctx context.Context, pool *pgxpool.Pool, userID, hash string) error {
	_, err := pool.Exec(ctx, `UPDATE users SET password_hash = $1 WHERE id = $2`, hash, userID)
	return err
}

func UpdateFingerInfo(ctx context.Context, pool *pgxpool.Pool, userID, displayName, office, homePhone string) error {
	_, err := pool.Exec(ctx,
		`UPDATE users SET display_name = $1, office = $2, home_phone = $3 WHERE id = $4`,
		displayName, office, homePhone, userID,
	)
	return err
}

func UpdateMesg(ctx context.Context, pool *pgxpool.Pool, userID string, on bool) error {
	_, err := pool.Exec(ctx, `UPDATE users SET mesg_on = $1 WHERE id = $2`, on, userID)
	return err
}

func UpdateBiff(ctx context.Context, pool *pgxpool.Pool, userID string, on bool) error {
	_, err := pool.Exec(ctx, `UPDATE users SET biff = $1 WHERE id = $2`, on, userID)
	return err
}

func UpdateVacation(ctx context.Context, pool *pgxpool.Pool, userID string, on bool, msg string) error {
	_, err := pool.Exec(ctx,
		`UPDATE users SET vacation = $1, vacation_message = $2 WHERE id = $3`,
		on, msg, userID,
	)
	return err
}

func AddSSHKey(ctx context.Context, pool *pgxpool.Pool, userID, keyData string) error {
	parts := strings.Fields(keyData)
	if len(parts) < 2 {
		return fmt.Errorf("invalid public key format")
	}
	keyType := parts[0]
	comment := ""
	if len(parts) > 2 {
		comment = strings.Join(parts[2:], " ")
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO ssh_keys (user_id, key_type, key_data, comment) VALUES ($1, $2, $3, $4)`,
		userID, keyType, keyData, comment,
	)
	return err
}

func GetSSHKeys(ctx context.Context, pool *pgxpool.Pool, userID string) ([]SSHKey, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, user_id, key_type, key_data, comment FROM ssh_keys WHERE user_id = $1`, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []SSHKey
	for rows.Next() {
		var k SSHKey
		if err := rows.Scan(&k.ID, &k.UserID, &k.KeyType, &k.KeyData, &k.Comment); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

func RecordLogin(ctx context.Context, pool *pgxpool.Pool, username, tty, fromAddr string) (string, error) {
	var id string
	err := pool.QueryRow(ctx,
		`INSERT INTO login_history (username, tty, from_addr) VALUES ($1, $2, $3) RETURNING id`,
		username, tty, fromAddr,
	).Scan(&id)
	return id, err
}

func RecordLogout(ctx context.Context, pool *pgxpool.Pool, loginID string) error {
	_, err := pool.Exec(ctx,
		`UPDATE login_history SET logged_out_at = NOW() WHERE id = $1`, loginID,
	)
	return err
}

func GetLoginHistory(ctx context.Context, pool *pgxpool.Pool, username string, limit int) ([]LoginRecord, error) {
	q := `SELECT id, username, tty, from_addr, logged_in_at, logged_out_at
		  FROM login_history`
	args := []any{limit}
	if username != "" {
		q += ` WHERE username = $2`
		args = append(args, username)
	}
	q += ` ORDER BY logged_in_at DESC LIMIT $1`

	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []LoginRecord
	for rows.Next() {
		var r LoginRecord
		if err := rows.Scan(&r.ID, &r.Username, &r.TTY, &r.FromAddr, &r.LoggedInAt, &r.LoggedOutAt); err != nil {
			return nil, err
		}
		records = append(records, r)
	}
	return records, rows.Err()
}

func GetMOTD(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	var body string
	err := pool.QueryRow(ctx, `SELECT body FROM motd WHERE id = 1`).Scan(&body)
	return body, err
}

func SetMOTD(ctx context.Context, pool *pgxpool.Pool, body string) error {
	_, err := pool.Exec(ctx,
		`UPDATE motd SET body = $1, updated_at = NOW() WHERE id = 1`, body,
	)
	return err
}

func GetRandomFortune(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	var body string
	err := pool.QueryRow(ctx,
		`SELECT body FROM fortunes WHERE status = 'approved' ORDER BY RANDOM() LIMIT 1`).Scan(&body)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return body, err
}

func GetSystemMessages(ctx context.Context, pool *pgxpool.Pool, userID string) ([]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT sm.id, sm.body
		FROM system_messages sm
		LEFT JOIN user_message_reads umr ON umr.message_id = sm.id AND umr.user_id = $1
		WHERE umr.message_id IS NULL
		  AND (sm.expires_at IS NULL OR sm.expires_at > NOW())
		ORDER BY sm.created_at ASC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids, bodies []string
	for rows.Next() {
		var id, body string
		if err := rows.Scan(&id, &body); err != nil {
			return nil, err
		}
		ids = append(ids, id)
		bodies = append(bodies, body)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Mark all as read in one statement
	if len(ids) > 0 {
		pool.Exec(ctx, `
			INSERT INTO user_message_reads (user_id, message_id)
			SELECT $1, unnest($2::uuid[])
			ON CONFLICT DO NOTHING`,
			userID, ids,
		)
	}
	return bodies, nil
}

func PostSystemMessage(ctx context.Context, pool *pgxpool.Pool, body string) error {
	_, err := pool.Exec(ctx, `INSERT INTO system_messages (body) VALUES ($1)`, body)
	return err
}

func CountSystemMessages(ctx context.Context, pool *pgxpool.Pool, userID string) (int, error) {
	var count int
	err := pool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM system_messages sm
		LEFT JOIN user_message_reads umr ON umr.message_id = sm.id AND umr.user_id = $1
		WHERE umr.message_id IS NULL
		  AND (sm.expires_at IS NULL OR sm.expires_at > NOW())`,
		userID,
	).Scan(&count)
	return count, err
}

func UserExists(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var count int
	err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	return count > 0, err
}

func UserCount(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var count int
	err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&count)
	return count, err
}

func GetPublicPage(ctx context.Context, pool *pgxpool.Pool, username string) (string, error) {
	u, err := GetUserByUsername(ctx, pool, username)
	if err != nil {
		return "", err
	}
	if u.PublicPage == "" {
		return "", fmt.Errorf("no public page set")
	}
	return u.PublicPage, nil
}

// CreateSession inserts a new session for userID and returns the token UUID string.
func CreateSession(ctx context.Context, pool *pgxpool.Pool, userID string) (string, error) {
	var token string
	err := pool.QueryRow(ctx,
		`INSERT INTO sessions (user_id) VALUES ($1) RETURNING token::text`, userID,
	).Scan(&token)
	return token, err
}

// GetSessionUser looks up the user for a valid, non-expired session token.
// Expired rows are ignored here and swept by the janitor (see StartJanitor),
// keeping this hot read path free of locking writes.
func GetSessionUser(ctx context.Context, pool *pgxpool.Pool, token string) (*User, error) {
	var userID string
	err := pool.QueryRow(ctx,
		`SELECT user_id::text FROM sessions WHERE token = $1 AND expires_at > NOW()`, token,
	).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return GetUserByID(ctx, pool, userID)
}

// DeleteSession removes a session by its token string.
func DeleteSession(ctx context.Context, pool *pgxpool.Pool, token string) error {
	_, err := pool.Exec(ctx, `DELETE FROM sessions WHERE token = $1`, token)
	return err
}
