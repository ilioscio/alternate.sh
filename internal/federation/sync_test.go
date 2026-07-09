package federation

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/ilioscio/alternate.sh/internal/assp"
)

// startSyncServer runs a federation server with mail/news handlers backed by
// simple in-memory state, returning the address and the state for assertions.
type syncState struct {
	mail     []MailDeliverRequest
	articles []WireArticle
	cancels  []string
	// vacationFor returns an auto-reply for this recipient, if any.
	vacationFor map[string]string
	// peerSeen records the authenticated peer name each handler observed.
	peerSeen string
}

func startSyncServer(t *testing.T, node string, st *syncState) string {
	t.Helper()
	tlsCfg, err := assp.SelfSignedConfig(node)
	if err != nil {
		t.Fatal(err)
	}
	secretFor := func(peer string) (string, bool) { return testSecret, peer == "client.test" }
	srv := NewServer(node, fakeSource{}, secretFor, tlsCfg)

	srv.OnMailDeliver = func(peer string, req MailDeliverRequest) MailDeliverResponse {
		st.peerSeen = peer
		if req.To == "ghost" {
			return MailDeliverResponse{OK: false, Reason: "no such user", Permanent: true}
		}
		st.mail = append(st.mail, req)
		return MailDeliverResponse{OK: true, Vacation: st.vacationFor[req.To]}
	}
	srv.OnNewsArticle = func(peer string, art WireArticle) NewsArticleResponse {
		st.peerSeen = peer
		if art.Group == "nowhere.local" {
			return NewsArticleResponse{OK: false, Reason: "no such newsgroup here"}
		}
		st.articles = append(st.articles, art)
		return NewsArticleResponse{OK: true}
	}
	srv.OnNewsCancel = func(peer string, originID string) {
		st.cancels = append(st.cancels, originID)
	}
	srv.OnNewsSince = func(peer string, since int64) NewsSinceResponse {
		// Serve a deterministic archive of 450 articles, batched.
		var batch []WireArticle
		for i := 0; i < 450; i++ {
			updated := int64((i + 1) * 1000)
			if updated <= since {
				continue
			}
			batch = append(batch, WireArticle{
				OriginID:  fmt.Sprintf("art-%03d", i),
				Group:     "alt.chat",
				Author:    "bob",
				Subject:   fmt.Sprintf("post %d", i),
				Body:      "body",
				CreatedAt: updated,
				UpdatedAt: updated,
			})
			if len(batch) == NewsBatchLimit {
				return NewsSinceResponse{Articles: batch, More: true}
			}
		}
		return NewsSinceResponse{Articles: batch}
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String()
}

func TestMailSendRoundTrip(t *testing.T) {
	st := &syncState{vacationFor: map[string]string{"vacationer": "gone fishing"}}
	addr := startSyncServer(t, "server.test", st)
	peer := Peer{Node: "server.test", Address: addr, Secret: testSecret}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := SendRemoteMail(ctx, "client.test", peer, MailDeliverRequest{
		From: "alice", To: "bob", Subject: "hello", Body: "over the wire",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK || resp.Vacation != "" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if len(st.mail) != 1 || st.mail[0].From != "alice" || st.mail[0].Subject != "hello" {
		t.Fatalf("delivered mail: %+v", st.mail)
	}
	if st.peerSeen != "client.test" {
		t.Fatalf("handler saw peer %q, want handshake identity", st.peerSeen)
	}

	// Permanent rejection.
	resp, err = SendRemoteMail(ctx, "client.test", peer, MailDeliverRequest{From: "alice", To: "ghost"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK || !resp.Permanent {
		t.Fatalf("ghost delivery: %+v", resp)
	}

	// Vacation auto-reply rides the response.
	resp, err = SendRemoteMail(ctx, "client.test", peer, MailDeliverRequest{From: "alice", To: "vacationer"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK || resp.Vacation != "gone fishing" {
		t.Fatalf("vacation delivery: %+v", resp)
	}
}

func TestNewsPushAndCancel(t *testing.T) {
	st := &syncState{}
	addr := startSyncServer(t, "server.test", st)
	peer := Peer{Node: "server.test", Address: addr, Secret: testSecret}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := PushArticle(ctx, "client.test", peer, WireArticle{
		OriginID: "a1", Group: "alt.chat", Author: "alice", Subject: "hi", Body: "b",
		ParentOriginNode: "server.test", ParentOriginID: "root-1",
	})
	if err != nil || !resp.OK {
		t.Fatalf("push: %+v, %v", resp, err)
	}
	if len(st.articles) != 1 || st.articles[0].ParentOriginID != "root-1" {
		t.Fatalf("stored: %+v", st.articles)
	}

	resp, err = PushArticle(ctx, "client.test", peer, WireArticle{OriginID: "a2", Group: "nowhere.local"})
	if err != nil || resp.OK {
		t.Fatalf("unknown group should be refused: %+v, %v", resp, err)
	}

	if err := PushCancel(ctx, "client.test", peer, "a1"); err != nil {
		t.Fatal(err)
	}
	if len(st.cancels) != 1 || st.cancels[0] != "a1" {
		t.Fatalf("cancels: %+v", st.cancels)
	}
}

func TestPullNewsSinceBatches(t *testing.T) {
	st := &syncState{}
	addr := startSyncServer(t, "server.test", st)
	peer := Peer{Node: "server.test", Address: addr, Secret: testSecret}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var got []WireArticle
	mark, err := PullNewsSince(ctx, "client.test", peer, time.Time{}, func(a WireArticle) error {
		got = append(got, a)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 450 {
		t.Fatalf("pulled %d articles, want 450 across batches", len(got))
	}
	if Micros(mark) != 450*1000 {
		t.Fatalf("mark = %d, want the last article's updated_at", Micros(mark))
	}

	// Resuming from the mark yields nothing new.
	got = nil
	mark2, err := PullNewsSince(ctx, "client.test", peer, mark, func(a WireArticle) error {
		got = append(got, a)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 || !mark2.Equal(mark) {
		t.Fatalf("resume pulled %d articles, mark %v (want none, unchanged)", len(got), mark2)
	}
}

func TestSyncVerbsRefusedWithoutHandlers(t *testing.T) {
	// The plain test server (federation_test.go) registers no sync handlers.
	addr := startTestServer(t, "server.test")
	peer := Peer{Node: "server.test", Address: addr, Secret: testSecret}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := SendRemoteMail(ctx, "client.test", peer, MailDeliverRequest{To: "bob"}); err == nil {
		t.Error("mail without handler: want error")
	}
	if _, err := PushArticle(ctx, "client.test", peer, WireArticle{Group: "alt.chat"}); err == nil {
		t.Error("news without handler: want error")
	}
}
