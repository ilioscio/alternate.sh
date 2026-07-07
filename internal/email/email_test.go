package email

import (
	"bufio"
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBuildMessageHeaders(t *testing.T) {
	msg, err := buildMessage("noreply@ilios.dev", "alternate.sh", "user@example.com", "Confirm your account", "Hello\nWorld")
	if err != nil {
		t.Fatal(err)
	}
	s := string(msg)

	for _, want := range []string{
		"From: alternate.sh <noreply@ilios.dev>\r\n",
		"To: user@example.com\r\n",
		"Subject: Confirm your account\r\n",
		"MIME-Version: 1.0\r\n",
		"Content-Type: text/plain; charset=utf-8\r\n",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("message missing header %q", want)
		}
	}
	// Body present with CRLF normalization, after the header/body separator.
	if !strings.Contains(s, "\r\n\r\nHello\r\nWorld\r\n") {
		t.Errorf("body not CRLF-normalized:\n%q", s)
	}
}

func TestBuildMessageRejectsHeaderInjection(t *testing.T) {
	_, err := buildMessage("noreply@ilios.dev", "", "victim@example.com\r\nBcc: evil@example.com", "Subj", "body")
	if err == nil {
		t.Error("header injection via To was not rejected")
	}
	_, err = buildMessage("noreply@ilios.dev", "", "a@b.com", "Subj\r\nX-Injected: 1", "body")
	if err == nil {
		t.Error("header injection via Subject was not rejected")
	}
}

func TestBuildMessageNoFromName(t *testing.T) {
	msg, _ := buildMessage("noreply@ilios.dev", "", "a@b.com", "s", "b")
	if !strings.Contains(string(msg), "From: noreply@ilios.dev\r\n") {
		t.Errorf("bare From header wrong:\n%s", msg)
	}
}

// mockSMTP is a minimal plaintext SMTP sink (no STARTTLS, no auth) used to
// exercise the full Send conversation. It records the DATA payload.
type mockSMTP struct {
	ln       net.Listener
	received chan string
}

func newMockSMTP(t *testing.T) *mockSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	m := &mockSMTP{ln: ln, received: make(chan string, 1)}
	go m.serve()
	return m
}

func (m *mockSMTP) addr() (host string, port string) {
	h, p, _ := net.SplitHostPort(m.ln.Addr().String())
	return h, p
}

func (m *mockSMTP) serve() {
	conn, err := m.ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	r := bufio.NewReader(conn)
	w := conn

	write := func(s string) { w.Write([]byte(s)) }
	write("220 mock ESMTP\r\n")

	var data strings.Builder
	inData := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		if inData {
			if line == ".\r\n" {
				inData = false
				m.received <- data.String()
				write("250 OK\r\n")
				continue
			}
			data.WriteString(line)
			continue
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			// Advertise no extensions (no STARTTLS) — sink is plaintext/no-auth.
			write("250 mock\r\n")
		case strings.HasPrefix(cmd, "MAIL FROM"):
			write("250 OK\r\n")
		case strings.HasPrefix(cmd, "RCPT TO"):
			write("250 OK\r\n")
		case cmd == "DATA":
			write("354 End data with <CR><LF>.<CR><LF>\r\n")
			inData = true
		case cmd == "QUIT":
			write("221 Bye\r\n")
			return
		default:
			write("250 OK\r\n")
		}
	}
}

func TestSendOverMockSMTP(t *testing.T) {
	m := newMockSMTP(t)
	host, port := m.addr()
	portNum, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}

	s := New(Config{
		Enabled: true,
		Host:    host,
		Port:    portNum,
		From:    "noreply@ilios.dev",
		// No username → no auth, matching the plaintext sink.
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Send(ctx, "user@example.com", "Hi", "body line"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case got := <-m.received:
		if !strings.Contains(got, "Subject: Hi") || !strings.Contains(got, "body line") {
			t.Errorf("sink did not receive expected message:\n%s", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for message at sink")
	}
}

func TestSendDisabled(t *testing.T) {
	s := New(Config{Enabled: false})
	if err := s.Send(context.Background(), "a@b.com", "s", "b"); err == nil {
		t.Error("Send should fail when disabled")
	}
}

func TestAuthRequiresTLS(t *testing.T) {
	// Sink advertises no STARTTLS; with a username set, Send must refuse.
	m := newMockSMTP(t)
	host, port := m.addr()
	portNum, _ := strconv.Atoi(port)

	s := New(Config{
		Enabled:  true,
		Host:     host,
		Port:     portNum,
		From:     "noreply@ilios.dev",
		Username: "noreply@ilios.dev",
	})
	err := s.Send(context.Background(), "a@b.com", "s", "b")
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Errorf("expected STARTTLS-required error, got %v", err)
	}
}
