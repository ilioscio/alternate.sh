package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"html/template"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/ilioscio/alternate.sh/internal/config"
	"github.com/ilioscio/alternate.sh/internal/db"
	"github.com/ilioscio/alternate.sh/internal/presence"
	"github.com/ilioscio/alternate.sh/internal/shell"
	webstatic "github.com/ilioscio/alternate.sh/web"
)

var upgrader = websocket.Upgrader{
	// Any origin may open the socket, but /ws requires a session token that a
	// cross-origin attacker page cannot read (it lives in localStorage, not a
	// cookie), so there is no CSRF vector to gate here.
	CheckOrigin: func(r *http.Request) bool { return true },
}

type WebSocketServer struct {
	cfg  *config.Config
	pool *pgxpool.Pool
	hub  *presence.Hub
	mux  *http.ServeMux
}

func NewWebSocket(cfg *config.Config, pool *pgxpool.Pool, hub *presence.Hub) *WebSocketServer {
	s := &WebSocketServer{cfg: cfg, pool: pool, hub: hub, mux: http.NewServeMux()}

	s.mux.HandleFunc("/api/login",  s.handleLogin)
	s.mux.HandleFunc("/api/logout", s.handleLogout)
	s.mux.HandleFunc("/ws",         s.handleWS)
	s.mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	// Catch-all: public pages at /~username, else serve embedded frontend.
	s.mux.HandleFunc("/", s.handleRoot)

	return s
}

func (s *WebSocketServer) ListenAndServe() error {
	addr := fmt.Sprintf(":%d", s.cfg.Web.Port)
	fmt.Printf("WebSocket server listening on %s\n", addr)

	srv := &http.Server{Addr: addr, Handler: s.mux}

	if s.cfg.Web.TLSCert != "" && s.cfg.Web.TLSKey != "" {
		return srv.ListenAndServeTLS(s.cfg.Web.TLSCert, s.cfg.Web.TLSKey)
	}
	return srv.ListenAndServe()
}

// ── /api/login ────────────────────────────────────────────────────────────────

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token       string `json:"token"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

func (s *WebSocketServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.Username = strings.TrimSpace(req.Username)

	u, err := db.GetUserByUsername(r.Context(), s.pool, req.Username)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)) != nil {
		jsonError(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	token, err := db.CreateSession(r.Context(), s.pool, u.ID)
	if err != nil {
		jsonError(w, "could not create session", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(loginResponse{
		Token:       token,
		Username:    u.Username,
		DisplayName: u.DisplayName,
	})
}

// ── /api/logout ───────────────────────────────────────────────────────────────

func (s *WebSocketServer) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := r.Header.Get("X-Token")
	if token != "" {
		db.DeleteSession(r.Context(), s.pool, token)
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── /ws ───────────────────────────────────────────────────────────────────────

func (s *WebSocketServer) handleWS(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "token required", http.StatusUnauthorized)
		return
	}

	u, err := db.GetSessionUser(r.Context(), s.pool, token)
	if err != nil {
		http.Error(w, "invalid or expired session", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Use a pipe so we can intercept text (resize) vs binary (terminal input).
	pr, pw := io.Pipe()

	tty       := s.hub.AllocateTTY()
	sessionID := fmt.Sprintf("ws-%s-%s", u.Username, tty)
	from      := remoteAddr(conn.RemoteAddr())

	ww := &wsWriter{conn: conn}
	sess := shell.NewSession(ctx, sessionID, tty, from,
		pr, ww, 24, 80,
		u, s.hub, s.pool, s.cfg,
	)

	// Run the shell in the background; cancel context when it exits.
	go func() {
		defer cancel()
		shell.Run(sess)
	}()

	// Read loop: dispatch binary frames to shell stdin; text frames to resize.
	for {
		msgType, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}
		switch msgType {
		case websocket.TextMessage:
			var ctrl struct {
				Type string `json:"type"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
			}
			if json.Unmarshal(msg, &ctrl) == nil && ctrl.Type == "resize" && ctrl.Cols > 0 && ctrl.Rows > 0 {
				sess.Resize(ctrl.Cols, ctrl.Rows)
			}
		default:
			pw.Write(msg)
		}
	}
	pw.Close()
}

// ── / (root + public pages) ───────────────────────────────────────────────────

var publicPageTmpl = template.Must(template.New("pub").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>~{{.Username}} — alternate.sh</title>
<style>
*{box-sizing:border-box;margin:0;padding:0}
html,body{background:#050a05;color:#33ff33;font-family:'Courier New',monospace;font-size:14px;padding:1.5rem}
h1{font-size:1rem;margin-bottom:0.5rem;letter-spacing:0.1em}
.sub{color:#1a7a1a;font-size:0.8rem;margin-bottom:1.5rem}
pre{white-space:pre-wrap;word-break:break-word;line-height:1.5;max-width:80ch}
a{color:#33ff33}
</style>
</head>
<body>
<h1>~{{.Username}}</h1>
<div class="sub">alternate.sh · <a href="/">login</a></div>
<pre>{{.Content}}</pre>
</body>
</html>
`))

func (s *WebSocketServer) handleRoot(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/~") {
		s.handlePublicPage(w, r)
		return
	}
	http.FileServerFS(webstatic.FS).ServeHTTP(w, r)
}

func (s *WebSocketServer) handlePublicPage(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimPrefix(r.URL.Path, "/~")
	username  = strings.TrimSuffix(username, "/")
	if username == "" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	content, err := db.GetPublicPage(r.Context(), s.pool, username)
	if err != nil {
		// Single generic 404 for both "no such user" and "no page set" so the
		// endpoint isn't an account-enumeration oracle.
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	publicPageTmpl.Execute(w, struct {
		Username string
		Content  string
	}{username, content})
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func jsonError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// wsWriter sends binary WebSocket frames; safe for concurrent use.
type wsWriter struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (w *wsWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.conn.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}
