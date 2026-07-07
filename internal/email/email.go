// Package email provides transactional email over SMTP submission
// (STARTTLS + AUTH PLAIN on 587). It is intentionally small and dependency-
// free so it can be lifted into other projects: construct a Sender from a
// Config and call Send. Credentials are read from a file (e.g. an agenix
// secret) at send time so secret rotation needs no restart.
package email

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"os"
	"strings"
	"time"
)

type Config struct {
	Enabled       bool
	Host          string
	Port          int
	Username      string // empty → no AUTH (dev/localhost catchers)
	From          string
	FromName      string
	PasswordFile  string
	SkipTLSVerify bool // TEST/localhost only
}

type Sender struct {
	cfg Config
}

func New(cfg Config) *Sender { return &Sender{cfg: cfg} }

// Enabled reports whether sending is configured.
func (s *Sender) Enabled() bool { return s.cfg.Enabled }

// Send delivers a plain-text UTF-8 message. It returns an error if email is
// disabled or the SMTP conversation fails.
func (s *Sender) Send(ctx context.Context, to, subject, body string) error {
	if !s.cfg.Enabled {
		return fmt.Errorf("email sending is disabled")
	}
	if s.cfg.Host == "" || s.cfg.From == "" {
		return fmt.Errorf("email: host and from must be configured")
	}

	msg, err := buildMessage(s.cfg.From, s.cfg.FromName, to, subject, body)
	if err != nil {
		return err
	}

	addr := net.JoinHostPort(s.cfg.Host, fmt.Sprintf("%d", s.cfg.Port))

	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("email: dial %s: %w", addr, err)
	}

	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("email: smtp client: %w", err)
	}
	defer c.Close()

	if ok, _ := c.Extension("STARTTLS"); ok {
		tlsCfg := &tls.Config{
			ServerName:         s.cfg.Host,
			InsecureSkipVerify: s.cfg.SkipTLSVerify,
		}
		if err := c.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("email: STARTTLS: %w", err)
		}
	} else if s.cfg.Username != "" {
		// Never send credentials over an unencrypted link.
		return fmt.Errorf("email: server does not support STARTTLS but auth is configured")
	}

	if s.cfg.Username != "" {
		pw, err := s.readPassword()
		if err != nil {
			return err
		}
		auth := smtp.PlainAuth("", s.cfg.Username, pw, s.cfg.Host)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("email: auth: %w", err)
		}
	}

	if err := c.Mail(s.cfg.From); err != nil {
		return fmt.Errorf("email: MAIL FROM: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("email: RCPT TO: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("email: DATA: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("email: write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("email: close body: %w", err)
	}
	return c.Quit()
}

func (s *Sender) readPassword() (string, error) {
	if s.cfg.PasswordFile == "" {
		return "", fmt.Errorf("email: username set but no password_file configured")
	}
	b, err := os.ReadFile(s.cfg.PasswordFile)
	if err != nil {
		return "", fmt.Errorf("email: read password file: %w", err)
	}
	return strings.TrimRight(string(b), "\r\n"), nil
}

// buildMessage assembles an RFC 5322 message with CRLF line endings. Header
// values are guarded against header injection (no CR/LF permitted).
func buildMessage(from, fromName, to, subject, body string) ([]byte, error) {
	for _, v := range []string{from, fromName, to, subject} {
		if strings.ContainsAny(v, "\r\n") {
			return nil, fmt.Errorf("email: header value contains a line break")
		}
	}

	fromHeader := from
	if fromName != "" {
		fromHeader = fmt.Sprintf("%s <%s>", fromName, from)
	}

	var b strings.Builder
	writeHeader(&b, "From", fromHeader)
	writeHeader(&b, "To", to)
	writeHeader(&b, "Subject", subject)
	writeHeader(&b, "Date", time.Now().Format(time.RFC1123Z))
	writeHeader(&b, "Message-ID", messageID(from))
	writeHeader(&b, "MIME-Version", "1.0")
	writeHeader(&b, "Content-Type", "text/plain; charset=utf-8")
	writeHeader(&b, "Content-Transfer-Encoding", "8bit")
	b.WriteString("\r\n")

	// Normalize body to CRLF and dot-stuff is handled by net/smtp's DataWriter.
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\n", "\r\n")
	b.WriteString(body)
	if !strings.HasSuffix(body, "\r\n") {
		b.WriteString("\r\n")
	}
	return []byte(b.String()), nil
}

func writeHeader(b *strings.Builder, k, v string) {
	b.WriteString(k)
	b.WriteString(": ")
	b.WriteString(v)
	b.WriteString("\r\n")
}

func messageID(from string) string {
	domain := "localhost"
	if at := strings.LastIndex(from, "@"); at >= 0 {
		domain = from[at+1:]
	}
	var buf [16]byte
	rand.Read(buf[:])
	return fmt.Sprintf("<%x.%d@%s>", buf, time.Now().UnixNano(), domain)
}
