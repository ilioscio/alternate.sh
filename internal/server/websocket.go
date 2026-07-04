package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/ilioscio/alternate.sh/internal/config"
	"github.com/ilioscio/alternate.sh/internal/db"
	"github.com/ilioscio/alternate.sh/internal/presence"
	"github.com/ilioscio/alternate.sh/internal/shell"
)

var upgrader = websocket.Upgrader{
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
	s.mux.HandleFunc("/ws", s.handleWS)
	s.mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
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

// handleWS upgrades an HTTP connection to WebSocket and runs a shell session.
// Query params: ?user=username&pass=password (Phase 1 auth; Phase 2 adds a proper login flow).
func (s *WebSocketServer) handleWS(w http.ResponseWriter, r *http.Request) {
	username := r.URL.Query().Get("user")
	password := r.URL.Query().Get("pass")

	if username == "" || password == "" {
		http.Error(w, "user and pass required", http.StatusUnauthorized)
		return
	}

	u, err := db.GetUserByUsername(r.Context(), s.pool, username)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	rw := newWSReadWriter(conn)
	tty := s.hub.AllocateTTY()
	sessionID := fmt.Sprintf("ws-%s-%s", username, tty)
	from := remoteAddr(conn.RemoteAddr())

	sess := shell.NewSession(ctx, sessionID, tty, from,
		rw, rw, 24, 80,
		u, s.hub, s.pool, s.cfg,
	)
	shell.Run(sess)
}

// wsReadWriter bridges a gorilla WebSocket connection to io.ReadWriter.
// Reads block waiting for binary/text frames; writes send binary frames.
type wsReadWriter struct {
	conn *websocket.Conn
	mu   sync.Mutex
	buf  []byte
}

func newWSReadWriter(conn *websocket.Conn) *wsReadWriter {
	return &wsReadWriter{conn: conn}
}

func (w *wsReadWriter) Read(p []byte) (int, error) {
	for len(w.buf) == 0 {
		_, msg, err := w.conn.ReadMessage()
		if err != nil {
			return 0, io.EOF
		}
		w.buf = msg
	}
	n := copy(p, w.buf)
	w.buf = w.buf[n:]
	return n, nil
}

func (w *wsReadWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.conn.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}
