package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/ilioscio/alternate.sh/internal/db"
	"github.com/ilioscio/alternate.sh/internal/valid"
)

// maxConfirmAttempts is how many wrong codes a pending signup tolerates before
// it is destroyed and the applicant must sign up again.
const maxConfirmAttempts = 5

type signupRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// handleSignup accepts a registration, stores a pending account, and emails a
// confirmation link + code. Responses avoid leaking whether an email is
// already registered; username conflicts are reported (usernames are public).
func (s *WebSocketServer) handleSignup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.email.Enabled() {
		jsonError(w, "signups are currently closed", http.StatusServiceUnavailable)
		return
	}

	ip := clientIP(r)
	if !s.signup.Allow(ip) {
		jsonError(w, "too many signups from your network; please try again later", http.StatusTooManyRequests)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req signupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if err := valid.ValidateUsername(req.Username); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := valid.ValidatePassword(req.Password); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	emailAddr, err := valid.ValidateEmail(req.Email)
	if err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		jsonError(w, "could not process signup", http.StatusInternalServerError)
		return
	}

	code := numericCode(6)
	token, err := db.CreatePendingAccount(r.Context(), s.pool, req.Username, emailAddr, string(hash), code, ip)
	if err != nil {
		switch {
		case errors.Is(err, db.ErrUsernameTaken):
			jsonError(w, "that username is taken", http.StatusConflict)
		case errors.Is(err, db.ErrEmailTaken):
			// Don't reveal that the email exists; look like success.
			writeJSON(w, http.StatusOK, map[string]string{
				"status":  "pending",
				"message": "Check your email to confirm your account.",
			})
		default:
			jsonError(w, "could not process signup", http.StatusInternalServerError)
		}
		return
	}

	if err := s.sendConfirmation(r.Context(), emailAddr, req.Username, token, code); err != nil {
		// Roll back the pending row so a resend isn't blocked and no orphan lingers.
		if p, e := db.GetPendingByUsername(r.Context(), s.pool, req.Username); e == nil {
			db.DeletePending(r.Context(), s.pool, p.ID)
		}
		jsonError(w, "could not send confirmation email; please try again later", http.StatusBadGateway)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "pending",
		"message": "Check your email to confirm your account.",
	})
}

// handleConfirmCode confirms a pending account via the emailed numeric code.
func (s *WebSocketServer) handleConfirmCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req struct {
		Username string `json:"username"`
		Code     string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	p, err := db.GetPendingByUsername(r.Context(), s.pool, req.Username)
	if err != nil {
		jsonError(w, "no pending signup found or it has expired", http.StatusNotFound)
		return
	}

	// Constant-time compare; count the attempt and destroy after too many.
	if subtle.ConstantTimeCompare([]byte(p.Code), []byte(req.Code)) != 1 {
		n, _ := db.IncrementPendingAttempts(r.Context(), s.pool, p.ID)
		if n >= maxConfirmAttempts {
			db.DeletePending(r.Context(), s.pool, p.ID)
			jsonError(w, "too many incorrect codes; please sign up again", http.StatusTooManyRequests)
			return
		}
		jsonError(w, "incorrect code", http.StatusUnauthorized)
		return
	}

	if _, err := db.ConfirmPendingAccount(r.Context(), s.pool, p); err != nil {
		jsonError(w, "could not confirm account", http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "confirmed",
		"message": "Account confirmed. You can now log in.",
	})
}

// handleConfirmLink confirms a pending account via the emailed link and shows
// a small HTML page.
func (s *WebSocketServer) handleConfirmLink(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		confirmPage(w, "Invalid link", "This confirmation link is missing its token.")
		return
	}
	p, err := db.GetPendingByToken(r.Context(), s.pool, token)
	if err != nil {
		confirmPage(w, "Link expired", "This confirmation link is invalid or has expired. Please sign up again.")
		return
	}
	if _, err := db.ConfirmPendingAccount(r.Context(), s.pool, p); err != nil {
		confirmPage(w, "Could not confirm", "That username or email was just taken. Please sign up again.")
		return
	}
	confirmPage(w, "Account confirmed", fmt.Sprintf("Welcome, %s. You can now return to the terminal and log in.", p.Username))
}

// sendConfirmation emails the applicant a link and a code. Branding uses the
// server hostname so there is a single place to rename the product later.
func (s *WebSocketServer) sendConfirmation(ctx context.Context, to, username, token, code string) error {
	brand := s.cfg.Server.Hostname
	base := s.cfg.Web.PublicURL
	link := fmt.Sprintf("%s/confirm?token=%s", base, token)

	subject := fmt.Sprintf("Confirm your %s account", brand)
	body := fmt.Sprintf(
		"Welcome to %s, %s.\n\n"+
			"Confirm your account by opening this link:\n\n  %s\n\n"+
			"Or enter this code in the signup screen:\n\n  %s\n\n"+
			"This request expires in 24 hours. If you didn't sign up, ignore this email.\n",
		brand, username, link, code,
	)

	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return s.email.Send(cctx, to, subject, body)
}

// numericCode returns a cryptographically random n-digit decimal string.
func numericCode(n int) string {
	const digits = "0123456789"
	b := make([]byte, n)
	rand.Read(b)
	for i := range b {
		b[i] = digits[int(b[i])%len(digits)]
	}
	return string(b)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

var confirmTmpl = template.Must(template.New("confirm").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>{{.Title}}</title>
<style>
html,body{background:#050a05;color:#33ff33;font-family:'Courier New',monospace;padding:2rem;line-height:1.6}
h1{font-size:1.1rem;letter-spacing:0.1em;margin-bottom:1rem}
a{color:#33ff33}
</style></head>
<body><h1>{{.Title}}</h1><p>{{.Body}}</p><p><a href="/">&larr; back to {{.Brand}}</a></p></body></html>`))

func confirmPage(w http.ResponseWriter, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	confirmTmpl.Execute(w, struct{ Title, Body, Brand string }{title, body, "terminal"})
}
