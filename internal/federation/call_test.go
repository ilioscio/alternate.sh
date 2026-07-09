package federation

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"github.com/ilioscio/alternate.sh/internal/assp"
	"github.com/ilioscio/alternate.sh/internal/av"
	"github.com/ilioscio/alternate.sh/internal/calls"
	"github.com/ilioscio/alternate.sh/internal/presence"
)

// callNode is one side's call infrastructure: signaling manager + rooms.
type callNode struct {
	mgr   *calls.Manager
	rooms *presence.RoomBroker
}

func newCallNode() *callNode {
	return &callNode{mgr: calls.NewManager(), rooms: presence.NewRoomBroker()}
}

// startCallServer runs a federation server whose OnCallOpen mirrors the
// production responder (ring → deferred answer → bridge), with the callee
// auto-answering when accept is signalled.
func startCallServer(t *testing.T, node string, cn *callNode, accept <-chan bool) string {
	t.Helper()
	tlsCfg, err := assp.SelfSignedConfig(node)
	if err != nil {
		t.Fatal(err)
	}
	secretFor := func(peer string) (string, bool) { return testSecret, peer == "client.test" }
	srv := NewServer(node, fakeSource{}, secretFor, tlsCfg)

	srv.OnCallOpen = func(peerNode string, req CallOpenRequest, ac *assp.Conn) {
		fromQ := req.From + "@" + peerNode
		params := req.Params.Clamp(128, 96, 24)
		c, err := cn.mgr.Offer(fromQ, req.Target, req.Media, params)
		if err != nil {
			WriteResponse(ac, CallOpenResponse{Accepted: false, Reason: err.Error()})
			ac.Close()
			return
		}
		frames := ReadFrames(ac)

		// Simulated callee: answers (or declines) when the test says so.
		go func() {
			select {
			case yes := <-accept:
				if yes {
					c.Accept()
				} else {
					c.End("declined")
				}
			case <-c.Ended():
			}
		}()

		select {
		case <-c.Accepted():
			pseudo, _, ok := cn.rooms.JoinID(c.RoomID(), []string{c.Caller, c.Callee}, "relay:"+fromQ, fromQ)
			if !ok {
				c.End("relay setup failed")
				ac.Close()
				return
			}
			WriteResponse(ac, CallOpenResponse{Accepted: true, Params: params})
			RelayCallRoomToStream(c, pseudo, ac, frames, calls.SourceCaller)
		case <-c.Ended():
			WriteResponse(ac, CallOpenResponse{Accepted: false, Reason: c.EndReason()})
			ac.Close()
		case <-frames:
			c.End("canceled by caller")
			ac.Close()
		}
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String()
}

// initiateTestCall runs the initiator side (mirroring cmd_call_remote) and
// returns the local call once accepted.
func initiateTestCall(t *testing.T, cn *callNode, peer Peer, media string) *calls.Call {
	t.Helper()
	c, err := cn.mgr.Offer("alice", "bob@server.test", media, calls.Params{Width: 128, Height: 96, FPS: 24})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ac, err := InitiateCall(ctx, "client.test", peer, CallOpenRequest{
		From: "alice", Target: "bob", Media: media, Params: c.Params,
	})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		<-c.Ended()
		ac.Close()
	}()
	frames := ReadFrames(ac)
	go func() {
		resp, err := AwaitCallAnswer(frames)
		if err != nil {
			c.End("connection lost")
			return
		}
		if !resp.Accepted {
			c.End(resp.Reason)
			return
		}
		c.Params = resp.Params
		pseudo, _, ok := cn.rooms.JoinID(c.RoomID(), []string{c.Caller, c.Callee}, "relay:alice", "bob@server.test")
		if !ok {
			c.End("relay setup failed")
			return
		}
		go RelayCallRoomToStream(c, pseudo, ac, frames, calls.SourceCallee)
		c.Accept()
	}()
	return c
}

func recvData(t *testing.T, m *presence.RoomMember, what string) []byte {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-m.Recv:
			if !ok {
				t.Fatalf("%s: room membership closed while waiting for data", what)
			}
			if ev.Kind == presence.EventData {
				return ev.Data
			}
		case <-deadline:
			t.Fatalf("%s: timed out waiting for data", what)
		}
	}
}

func TestCrossNodeCallMediaFlow(t *testing.T) {
	callerNode := newCallNode()
	calleeNode := newCallNode()
	accept := make(chan bool, 1)
	accept <- true
	addr := startCallServer(t, "server.test", calleeNode, accept)
	peer := Peer{Node: "server.test", Address: addr, Secret: testSecret}

	c := initiateTestCall(t, callerNode, peer, calls.MediaAV)
	select {
	case <-c.Accepted():
	case <-c.Ended():
		t.Fatalf("call ended before accept: %s", c.EndReason())
	case <-time.After(5 * time.Second):
		t.Fatal("call never accepted")
	}

	// Attach the "browsers": local room members on each node.
	callerBrowser, _, ok := callerNode.rooms.JoinID(c.RoomID(), []string{"alice", "bob@server.test"}, "ws-alice", "alice")
	if !ok {
		t.Fatal("caller browser join failed")
	}
	remote := calleeNode.mgr.ForUser("bob")
	if remote == nil {
		t.Fatal("no call registered on the callee node")
	}
	calleeBrowser, _, ok := calleeNode.rooms.JoinID(remote.RoomID(), []string{remote.Caller, "bob"}, "ws-bob", "bob")
	if !ok {
		t.Fatal("callee browser join failed")
	}

	// Caller → callee: a real dithered keyframe, decodable on arrival.
	enc := &av.VideoEncoder{Source: 0}
	frame := av.DitherBlueNoise(bytes.Repeat([]byte{128}, 128*96), 128, 96)
	pkt := enc.Encode(frame)
	callerBrowser.Send(pkt)

	got := recvData(t, calleeBrowser, "callee video")
	if !bytes.Equal(got, pkt) {
		t.Fatal("video packet mutated in transit")
	}
	p, err := av.ParsePacket(got)
	if err != nil {
		t.Fatal(err)
	}
	dec := &av.VideoDecoder{}
	bm, err := dec.Decode(p)
	if err != nil || bm == nil || !bm.Equal(frame) {
		t.Fatalf("relayed frame does not decode to the original (err=%v)", err)
	}

	// Callee → caller: a real audio chunk.
	aenc := &av.AudioEncoder{Source: 1}
	apkt, err := aenc.EncodeChunk(make([]int16, av.ChunkSamples))
	if err != nil {
		t.Fatal(err)
	}
	calleeBrowser.Send(apkt)
	agot := recvData(t, callerBrowser, "caller audio")
	if !bytes.Equal(agot, apkt) {
		t.Fatal("audio packet mutated in transit")
	}
	ap, _ := av.ParsePacket(agot)
	if _, err := av.DecodeAudio(ap); err != nil {
		t.Fatalf("relayed audio does not decode: %v", err)
	}

	// A spoofed source id is dropped by the bridge: send a bogus packet from
	// the caller stamped as the callee, then a good one; only the good one
	// arrives.
	spoof := append([]byte{0x03, 1, 0, 0}, apkt[4:]...)
	callerBrowser.Send(spoof)
	good := enc.Encode(av.DitherBlueNoise(bytes.Repeat([]byte{90}, 128*96), 128, 96))
	callerBrowser.Send(good)
	if got := recvData(t, calleeBrowser, "callee post-spoof"); !bytes.Equal(got, good) {
		t.Fatal("spoofed-source packet crossed the bridge")
	}

	// Hangup propagates: the caller ends; the callee node's call ends too.
	c.End("hung up")
	select {
	case <-remote.Ended():
	case <-time.After(5 * time.Second):
		t.Fatal("hangup did not propagate to the callee node")
	}
}

func TestCrossNodeCallDeclined(t *testing.T) {
	callerNode := newCallNode()
	calleeNode := newCallNode()
	accept := make(chan bool, 1)
	accept <- false
	addr := startCallServer(t, "server.test", calleeNode, accept)
	peer := Peer{Node: "server.test", Address: addr, Secret: testSecret}

	c := initiateTestCall(t, callerNode, peer, calls.MediaAV)
	select {
	case <-c.Ended():
		if c.EndReason() != "declined" {
			t.Fatalf("EndReason = %q, want declined", c.EndReason())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("declined call never ended on the caller side")
	}
}

func TestCrossNodeCallCanceledByCaller(t *testing.T) {
	callerNode := newCallNode()
	calleeNode := newCallNode()
	accept := make(chan bool) // never answered
	addr := startCallServer(t, "server.test", calleeNode, accept)
	peer := Peer{Node: "server.test", Address: addr, Secret: testSecret}

	c := initiateTestCall(t, callerNode, peer, calls.MediaAV)

	// Give the ring a moment to register on the callee node, then cancel.
	deadline := time.After(5 * time.Second)
	var remote *calls.Call
	for remote == nil {
		remote = calleeNode.mgr.ForUser("bob")
		select {
		case <-deadline:
			t.Fatal("ring never registered on callee node")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	c.End("canceled")
	select {
	case <-remote.Ended():
		if remote.EndReason() != "canceled by caller" {
			t.Fatalf("remote EndReason = %q", remote.EndReason())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not propagate to the callee node")
	}
}
