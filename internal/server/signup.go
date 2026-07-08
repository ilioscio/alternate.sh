package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
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
	_, err = db.CreatePendingAccount(r.Context(), s.pool, req.Username, emailAddr, string(hash), code, ip)
	if err != nil {
		switch {
		case errors.Is(err, db.ErrUsernameTaken):
			jsonError(w, "that username is taken", http.StatusConflict)
		case errors.Is(err, db.ErrEmailTaken):
			// Don't reveal that the email exists in the API response. Instead
			// notify the real owner at that address — an attacker probing
			// someone else's email never receives this, so enumeration is still
			// prevented. Best-effort and synchronous so response timing stays
			// close to the normal (email-sending) path.
			s.sendAlreadyRegistered(r.Context(), emailAddr)
			writeJSON(w, http.StatusOK, map[string]string{
				"status":  "pending",
				"message": "Check your email to confirm your account.",
			})
		default:
			jsonError(w, "could not process signup", http.StatusInternalServerError)
		}
		return
	}

	if err := s.sendConfirmation(r.Context(), emailAddr, req.Username, code); err != nil {
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

// sendConfirmation emails the applicant a 6-digit confirmation code. Branding
// uses the server hostname so there is a single place to rename the product.
func (s *WebSocketServer) sendConfirmation(ctx context.Context, to, username, code string) error {
	brand := s.cfg.Server.Hostname

	subject := fmt.Sprintf("Confirm your %s account", brand)
	body := fmt.Sprintf(
		"Welcome to %s, %s.\n\n"+
			"Enter this code in the signup screen to confirm your account:\n\n  %s\n\n"+
			"This request expires in 24 hours. If you didn't sign up, ignore this email.\n",
		brand, username, code,
	)

	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return s.email.Send(cctx, to, subject, body)
}

// sendAlreadyRegistered tells the owner of an already-registered address that
// someone attempted to sign up with it. It is best-effort: send errors are
// ignored so the caller's response never reveals whether the email exists.
func (s *WebSocketServer) sendAlreadyRegistered(ctx context.Context, to string) {
	brand := s.cfg.Server.Hostname
	subject := fmt.Sprintf("You already have a %s account", brand)
	body := fmt.Sprintf(
		"Someone just tried to sign up for %s using this email address, "+
			"but it already has an account.\n\n"+
			"If this was you: no new account was created — just log in with your "+
			"existing username. If you've forgotten your login, contact an admin.\n\n"+
			"If this wasn't you, you can safely ignore this message.\n",
		brand,
	)

	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	s.email.Send(cctx, to, subject, body)
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
