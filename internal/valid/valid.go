// Package valid holds input validation shared by admin account creation and
// public self-signup. Centralizing it keeps the rules identical across every
// entry point — important now that untrusted users can create accounts.
package valid

import (
	"fmt"
	"net/mail"
	"regexp"
	"strings"
)

const (
	MinUsernameLen = 2
	MaxUsernameLen = 32
	MinPasswordLen = 8
	MaxPasswordLen = 128
	MaxEmailLen    = 254 // RFC 5321 maximum
)

// Usernames are lowercase, start and end with alphanumerics, and may contain
// dashes/underscores internally. Lowercase-only makes the stored form
// canonical, avoiding alice/Alice collisions.
var usernameRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9_-]{0,30}[a-z0-9])?$`)

// reserved are names that must never be registered by users. They collide with
// system roles, the unauthenticated flows, or common role addresses.
var reserved = map[string]bool{
	"guest": true, "anonymous": true, "anon": true,
	"root": true, "admin": true, "administrator": true, "sysop": true,
	"system": true, "daemon": true, "operator": true, "moderator": true,
	"postmaster": true, "webmaster": true, "hostmaster": true, "abuse": true,
	"noreply": true, "no-reply": true, "mailer-daemon": true, "mailerdaemon": true,
	"alternate": true, "alternate-sh": true, "support": true, "help": true,
	"info": true, "security": true, "null": true, "none": true, "undefined": true,
}

// ValidateUsername checks a proposed username. The returned error's text is
// safe to show to the user.
func ValidateUsername(name string) error {
	if len(name) < MinUsernameLen || len(name) > MaxUsernameLen {
		return fmt.Errorf("username must be %d-%d characters", MinUsernameLen, MaxUsernameLen)
	}
	if !usernameRe.MatchString(name) {
		return fmt.Errorf("username may contain only lowercase letters, digits, '-' and '_', and must start and end with a letter or digit")
	}
	if reserved[name] {
		return fmt.Errorf("that username is reserved")
	}
	return nil
}

// ValidatePassword enforces a length band. The maximum matters because bcrypt
// ignores input past 72 bytes and an unbounded password wastes hashing work.
func ValidatePassword(pw string) error {
	if len(pw) < MinPasswordLen {
		return fmt.Errorf("password must be at least %d characters", MinPasswordLen)
	}
	if len(pw) > MaxPasswordLen {
		return fmt.Errorf("password must be at most %d characters", MaxPasswordLen)
	}
	return nil
}

// NormalizeEmail lowercases and trims an address for storage/comparison.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// ValidateEmail checks basic address syntax and length, and rejects known
// disposable/throwaway domains. It returns the normalized address on success.
func ValidateEmail(email string) (string, error) {
	email = NormalizeEmail(email)
	if len(email) == 0 || len(email) > MaxEmailLen {
		return "", fmt.Errorf("invalid email address")
	}
	addr, err := mail.ParseAddress(email)
	if err != nil {
		return "", fmt.Errorf("invalid email address")
	}
	// ParseAddress accepts display names; require a bare address.
	if addr.Address != email {
		return "", fmt.Errorf("invalid email address")
	}
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return "", fmt.Errorf("invalid email address")
	}
	domain := email[at+1:]
	// Require a dotted, deliverable domain (rejects single-label like "a@b").
	if !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return "", fmt.Errorf("invalid email address")
	}
	if disposableDomains[domain] {
		return "", fmt.Errorf("disposable email addresses are not allowed")
	}
	return email, nil
}
