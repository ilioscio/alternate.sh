package shell

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/ilioscio/alternate.sh/internal/db"
	"github.com/ilioscio/alternate.sh/internal/presence"
)

// cmdTalk implements talk and ytalk: split-screen, character-by-character,
// ephemeral conversation in the classic style. A room is keyed by its full
// participant set, so each participant runs the same command with the other
// names and lands in the same room. Ctrl+C leaves.
func cmdTalk(s *Session, args []string) error {
	if len(args) == 0 {
		usageError(s, "talk", "<user> [user...]")
		return nil
	}

	// Unique peer list, excluding self.
	seen := map[string]bool{s.User.Username: true}
	var others []string
	for _, u := range args {
		if !seen[u] {
			seen[u] = true
			others = append(others, u)
		}
	}
	if len(others) == 0 {
		s.Println("talk: you can't talk to yourself")
		return nil
	}

	// All participants must be real users.
	for _, u := range others {
		if _, err := db.GetUserByUsername(s.ctx, s.db, u); err != nil {
			s.Printf("talk: %s: no such user\r\n", u)
			return nil
		}
	}

	participants := append([]string{s.User.Username}, others...)
	member, peers, ok := s.hub.Rooms.Join(participants, s.ID, s.User.Username)
	if !ok {
		s.Println("talk: could not join session")
		return nil
	}

	// Invite participants who aren't in the room yet.
	present := map[string]bool{}
	for _, p := range peers {
		present[p] = true
	}
	reachable := len(peers)
	for _, u := range others {
		if present[u] {
			continue
		}
		respond := respondCommand(participants, u)
		n := s.hub.Send(u, presence.WriteNotice{
			Kind:    presence.NoticeTalk,
			From:    s.User.Username,
			Message: fmt.Sprintf("talk: connection requested by %s. Respond with: %s", s.User.Username, respond),
		})
		if n == 0 {
			if len(s.hub.FindByUsername(u)) == 0 {
				s.Printf("talk: %s is not logged in\r\n", u)
			} else {
				s.Printf("talk: %s has messages turned off\r\n", u)
			}
			continue
		}
		reachable++
	}
	if reachable == 0 {
		member.Leave()
		s.Println("talk: no one to talk to")
		return nil
	}

	runTalkUI(s, member, peers)

	s.Write([]byte("\x1b[2J\x1b[H"))
	s.Println("[talk session ended]")
	return nil
}

// respondCommand builds the exact command the invitee should type: talk with
// every participant except themselves.
func respondCommand(participants []string, invitee string) string {
	var rest []string
	for _, p := range participants {
		if p != invitee {
			rest = append(rest, p)
		}
	}
	sort.Strings(rest)
	return "talk " + strings.Join(rest, " ")
}

// ── Split-screen renderer ─────────────────────────────────────────────────────

type talkPane struct {
	sessionID string
	label     string
	top       int // absolute 1-based row of the first content line
	height    int // content rows
	row, col  int // cursor within content, 0-based
	left      bool
}

type talkUI struct {
	mu    sync.Mutex
	s     *Session
	rows  int
	cols  int
	self  *talkPane
	peers []*talkPane // join order
}

func runTalkUI(s *Session, member *presence.RoomMember, initialPeers []string) {
	rows, cols := s.Size()
	ui := &talkUI{s: s, rows: rows, cols: cols}
	ui.self = &talkPane{sessionID: s.ID, label: "you"}
	for _, p := range initialPeers {
		// Session IDs of initial peers are unknown until they send something;
		// label lookup by username happens on first event. Reserve panes by name.
		ui.peers = append(ui.peers, &talkPane{sessionID: "user:" + p, label: p})
	}
	ui.redraw()

	// Render events from peers until our Recv closes (i.e. we leave).
	renderDone := make(chan struct{})
	go func() {
		defer close(renderDone)
		for ev := range member.Recv {
			switch ev.Kind {
			case presence.EventJoin:
				ui.addPeer(ev.SessionID, ev.From)
			case presence.EventLeave:
				ui.markLeft(ev.SessionID)
			case presence.EventData:
				ui.paneData(ui.peerPane(ev.SessionID, ev.From), ev.Data)
			}
		}
	}()

	// Input loop: forward everything; Ctrl+C leaves.
	buf := make([]byte, 256)
	for {
		n, err := s.r.Read(buf)
		if err != nil {
			break
		}
		chunk := buf[:n]
		if i := bytes.IndexByte(chunk, 0x03); i >= 0 {
			if i > 0 {
				member.Send(chunk[:i])
				ui.paneData(ui.self, chunk[:i])
			}
			break
		}
		member.Send(chunk)
		ui.paneData(ui.self, chunk)
	}

	member.Leave()
	<-renderDone
}

// peerPane finds (or creates, for peers present before we joined) the pane
// for a session, matching reserved panes by username on first contact.
func (ui *talkUI) peerPane(sessionID, username string) *talkPane {
	ui.mu.Lock()
	for _, p := range ui.peers {
		if p.sessionID == sessionID {
			ui.mu.Unlock()
			return p
		}
	}
	// First data from a peer who was here before us: claim the reserved pane.
	for _, p := range ui.peers {
		if p.sessionID == "user:"+username {
			p.sessionID = sessionID
			ui.mu.Unlock()
			return p
		}
	}
	ui.mu.Unlock()
	ui.addPeer(sessionID, username)
	ui.mu.Lock()
	defer ui.mu.Unlock()
	return ui.peers[len(ui.peers)-1]
}

func (ui *talkUI) addPeer(sessionID, username string) {
	ui.mu.Lock()
	for _, p := range ui.peers {
		if p.sessionID == sessionID {
			ui.mu.Unlock()
			return
		}
		// A reserved pane for this username that hasn't been claimed yet.
		if p.sessionID == "user:"+username {
			p.sessionID = sessionID
			ui.mu.Unlock()
			ui.redraw()
			return
		}
	}
	ui.peers = append(ui.peers, &talkPane{sessionID: sessionID, label: username})
	ui.mu.Unlock()
	ui.redraw()
}

func (ui *talkUI) markLeft(sessionID string) {
	ui.mu.Lock()
	for _, p := range ui.peers {
		if p.sessionID == sessionID && !p.left {
			p.left = true
			p.label += " (left)"
		}
	}
	ui.mu.Unlock()
	ui.redraw()
}

// redraw lays out the screen: title row, then one pane per participant
// (self first). Pane content is ephemeral and cleared on re-layout.
func (ui *talkUI) redraw() {
	ui.mu.Lock()
	defer ui.mu.Unlock()

	panes := append([]*talkPane{ui.self}, ui.peers...)
	usable := ui.rows - 1 // minus title row
	per := usable / len(panes)
	if per < 2 {
		per = 2 // degenerate small terminal: let it overflow rather than divide by zero
	}

	var sb strings.Builder
	sb.WriteString("\x1b[2J\x1b[H")

	title := "talk — Ctrl+C to leave"
	var names []string
	for _, p := range ui.peers {
		names = append(names, p.label)
	}
	if len(names) > 0 {
		title = "talk with " + strings.Join(names, ", ") + " — Ctrl+C to leave"
	} else {
		title = "talk — waiting for others to join... Ctrl+C to leave"
	}
	fmt.Fprintf(&sb, "\x1b[7m%-*s\x1b[0m", ui.cols, truncate(title, ui.cols))

	row := 2
	for _, p := range panes {
		header := "── " + p.label + " "
		if len(header) < ui.cols {
			header += strings.Repeat("─", ui.cols-len([]rune(header)))
		}
		fmt.Fprintf(&sb, "\x1b[%d;1H\x1b[2m%s\x1b[0m", row, truncate(header, ui.cols))
		p.top = row + 1
		p.height = per - 1
		p.row, p.col = 0, 0
		row += per
	}

	// Park cursor in our own pane.
	fmt.Fprintf(&sb, "\x1b[%d;1H", ui.self.top)
	ui.s.Write([]byte(sb.String()))
}

// paneData renders a chunk of bytes into a pane: printable runs written
// contiguously, newline moves down, wrap-to-top when the pane fills
// (classic talk behavior), backspace erases. The cursor is parked back in
// our own pane afterwards so local typing looks natural.
func (ui *talkUI) paneData(p *talkPane, data []byte) {
	if p == nil {
		return
	}
	ui.mu.Lock()
	defer ui.mu.Unlock()

	var sb strings.Builder
	flushRun := func(run []byte) {
		for len(run) > 0 {
			space := ui.cols - p.col
			if space <= 0 {
				ui.paneNewline(&sb, p)
				space = ui.cols
			}
			n := len(run)
			if n > space {
				n = space
			}
			fmt.Fprintf(&sb, "\x1b[%d;%dH%s", p.top+p.row, p.col+1, run[:n])
			p.col += n
			run = run[n:]
		}
	}

	var run []byte
	for _, b := range data {
		switch {
		case b >= 0x20 && b < 0x7f:
			run = append(run, b)
		case b == '\r':
			// Raw-mode Enter. (A following \n, if any, is ignored below.)
			flushRun(run)
			run = nil
			ui.paneNewline(&sb, p)
		case b == '\n':
			// Ignore: raw terminals send \r for Enter; a stray \n after \r
			// would otherwise produce a double newline.
		case b == 0x7f || b == 0x08:
			flushRun(run)
			run = nil
			if p.col > 0 {
				p.col--
				fmt.Fprintf(&sb, "\x1b[%d;%dH ", p.top+p.row, p.col+1)
			}
		}
	}
	flushRun(run)

	// Park cursor at our own input position.
	fmt.Fprintf(&sb, "\x1b[%d;%dH", ui.self.top+ui.self.row, ui.self.col+1)
	ui.s.Write([]byte(sb.String()))
}

// paneNewline advances to the next line, wrapping to a cleared pane top when
// the pane is full — the classic talk behavior instead of scrolling.
func (ui *talkUI) paneNewline(sb *strings.Builder, p *talkPane) {
	p.row++
	p.col = 0
	if p.row >= p.height {
		p.row = 0
		for i := 0; i < p.height; i++ {
			fmt.Fprintf(sb, "\x1b[%d;1H\x1b[2K", p.top+i)
		}
	}
}
