package shell

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/ilioscio/alternate.sh/internal/calls"
	"github.com/ilioscio/alternate.sh/internal/config"
	"github.com/ilioscio/alternate.sh/internal/db"
	"github.com/ilioscio/alternate.sh/internal/presence"
)

// cmdCall implements `call [-a] <user[@host]>` — live A/V calls (DESIGN.md
// §9). The terminal is the control surface: it rings, shows status, and
// Ctrl+C hangs up; the media surface is the browser's call panel, driven by
// JSON control messages on the terminal WebSocket. Answering mirrors talk's
// symmetric model: the callee types `call <caller>` back.
//
// Media is web-only. SSH users can neither place nor join a call's media;
// they get text explanations instead (the fork §9.1 anticipated).

// CallStartMsg tells the web client to open its call panel and connect a
// media WebSocket for the given call.
type CallStartMsg struct {
	Type   string       `json:"type"` // "call-start"
	CallID string       `json:"callId"`
	Role   string       `json:"role"` // "caller" | "callee"
	Source uint8        `json:"source"`
	Peer   string       `json:"peer"`
	Media  string       `json:"media"` // "av" | "audio"
	Params calls.Params `json:"params"`
}

// CallEndMsg tells the web client to tear its call panel down.
type CallEndMsg struct {
	Type   string `json:"type"` // "call-end"
	CallID string `json:"callId"`
	Reason string `json:"reason"`
}

func cmdCall(s *Session, args []string) error {
	media := calls.MediaAV
	decline := false
	var target string
	for _, a := range args {
		switch {
		case a == "-a":
			media = calls.MediaAudio
		case a == "-r":
			decline = true
		case strings.HasPrefix(a, "-"):
			usageError(s, "call", "[-a|-r] <user[@host]>")
			return nil
		case target != "":
			usageError(s, "call", "[-a|-r] <user[@host]>")
			return nil
		default:
			target = a
		}
	}
	if target == "" {
		usageError(s, "call", "[-a|-r] <user[@host]>")
		return nil
	}

	if !s.cfg.Calls.Enabled {
		s.Println("call: calls are disabled on this node")
		return nil
	}

	// Declining is pure signaling, so it works from any tier — an SSH user
	// can silence a ring even though media itself is web-only. For a
	// cross-node call, ending the local call makes the federation handler
	// relay "declined" back to the caller's node.
	if decline {
		c := s.hub.Calls.PendingFor(s.User.Username, target)
		if c == nil {
			s.Printf("call: no incoming call from %s\r\n", target)
			return nil
		}
		c.End("declined")
		s.Printf("Call from %s declined.\r\n", target)
		return nil
	}

	if !s.IsWeb() {
		s.Println("call: calls carry live audio/video, which needs the web client.")
		s.Printf("      Log in at the web frontend to place or answer calls;\r\n")
		s.Printf("      ssh remains the classic text tier ('call -r <user>' declines a ring).\r\n")
		return nil
	}

	if strings.Contains(target, "@") {
		return callRemote(s, target, media)
	}

	if target == s.User.Username {
		s.Println("call: you can't call yourself")
		return nil
	}
	if _, err := db.GetUserByUsername(s.ctx, s.db, target); err != nil {
		s.Printf("call: %s: no such user\r\n", target)
		return nil
	}

	// Answer path: a pending offer from the target means this is the callee
	// typing the symmetric `call <caller>`.
	if c := s.hub.Calls.PendingFor(s.User.Username, target); c != nil {
		if !c.Accept() {
			s.Println("call: too late — that call already ended")
			return nil
		}
		runCall(s, c, calls.SourceCallee, c.Caller)
		return nil
	}

	// Offer path.
	c, err := s.hub.Calls.Offer(s.User.Username, target, media, callParams(s.cfg))
	switch {
	case errors.Is(err, calls.ErrBusy):
		if mine := s.hub.Calls.ForUser(s.User.Username); mine != nil {
			s.Println("call: you are already in a call")
		} else {
			s.Printf("call: %s is busy\r\n", target)
		}
		return nil
	case errors.Is(err, calls.ErrRateLimit):
		s.Println("call: " + err.Error())
		return nil
	case err != nil:
		s.Println("call: " + err.Error())
		return nil
	}

	kind := "video call"
	if media == calls.MediaAudio {
		kind = "voice call"
	}
	notified := s.hub.Send(target, presence.WriteNotice{
		Kind: presence.NoticeCall,
		From: s.User.Username,
		Message: fmt.Sprintf("Incoming %s from %s. Type 'call %s' to answer (web client), or 'call -r %s' to decline.",
			kind, s.User.Username, s.User.Username, s.User.Username),
	})
	if notified == 0 {
		c.End("unreachable")
		if len(s.hub.FindByUsername(target)) == 0 {
			s.Printf("call: %s is not logged in\r\n", target)
		} else {
			s.Printf("call: %s has messages turned off\r\n", target)
		}
		return nil
	}

	s.Printf("Ringing %s... (Ctrl+C to cancel)\r\n", target)
	runCall(s, c, calls.SourceCaller, c.Callee)
	return nil
}

// callParams derives this node's media parameters from config, normalized to
// the codec's floors and alignment (the config values are also the ceiling,
// so Clamp against themselves enforces bounds and the multiple-of-8 width).
func callParams(cfg *config.Config) calls.Params {
	p := calls.Params{Width: cfg.Calls.Width, Height: cfg.Calls.Height, FPS: cfg.Calls.FPS}
	return p.Clamp(p.Width, p.Height, p.FPS)
}

// runCall drives the terminal side of a call from the moment it exists
// locally: ringing (caller) through active to ended.
//
// Input discipline: this function's main loop is the session's only reader.
// A watcher goroutine reacts to signaling (accept, remote hangup) but never
// touches the input stream, so the REPL's readline can safely resume the
// moment we return. When the remote side ends the call, the read is blocked
// on the user's keyboard — the watcher prints a "press Enter" prompt and the
// next keystroke releases the loop.
func runCall(s *Session, c *calls.Call, source uint8, peer string) {
	var localEnd atomic.Bool
	localDone := make(chan struct{})
	watcherDone := make(chan struct{})

	go func() {
		defer close(watcherDone)
		if source == calls.SourceCaller {
			select {
			case <-c.Accepted():
				startCallUI(s, c, "caller", source, peer)
			case <-c.Ended():
				if !localEnd.Load() {
					s.Printf("\r\ncall: %s — press Enter\r\n", c.EndReason())
				}
				return
			case <-localDone:
				return
			}
		} else {
			startCallUI(s, c, "callee", source, peer)
		}
		select {
		case <-c.Ended():
			if !localEnd.Load() {
				s.Printf("\r\n[call ended: %s] — press Enter\r\n", c.EndReason())
			}
		case <-localDone:
		}
	}()

	buf := make([]byte, 64)
	for {
		n, err := s.r.Read(buf)
		if err != nil {
			localEnd.Store(true)
			c.End("connection lost")
			break
		}
		// If the call already ended, this keystroke is the "press Enter"
		// acknowledgement.
		ended := false
		select {
		case <-c.Ended():
			ended = true
		default:
		}
		if ended {
			break
		}
		if bytes.IndexByte(buf[:n], 0x03) >= 0 {
			localEnd.Store(true)
			if c.Active() {
				c.End("hung up")
			} else {
				c.End("canceled")
			}
			break
		}
	}
	close(localDone)
	<-watcherDone

	s.SendControl(CallEndMsg{Type: "call-end", CallID: c.ID, Reason: c.EndReason()})
	if localEnd.Load() {
		s.Printf("[call ended: %s]\r\n", c.EndReason())
	}
}

// startCallUI transitions the terminal and browser into the active call.
func startCallUI(s *Session, c *calls.Call, role string, source uint8, peer string) {
	s.hub.SetState(s.ID, "on a call with "+peer)
	s.SendControl(CallStartMsg{
		Type:   "call-start",
		CallID: c.ID,
		Role:   role,
		Source: source,
		Peer:   peer,
		Media:  c.Media,
		Params: c.Params,
	})
	what := "video panel"
	if c.Media == calls.MediaAudio {
		what = "voice channel"
	}
	s.Printf("\r\n[call connected: %s] The %s opens beside this terminal. Ctrl+C to hang up.\r\n", peer, what)
}
