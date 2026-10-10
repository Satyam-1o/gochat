package room

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	"gochat/internal/identity"
	"gochat/internal/node"
	"gochat/internal/store"
)

func testNode(t *testing.T, ctx context.Context) *node.Node {
	t.Helper()
	priv, err := identity.LoadOrCreate(filepath.Join(t.TempDir(), "node.key"))
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	nd, err := node.New(ctx, node.Options{
		PrivKey:     priv,
		ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"},
		MDNS:        false,
	})
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	t.Cleanup(func() { nd.Close() })
	return nd
}

func testStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	return s
}

func connectNodes(t *testing.T, a, b *node.Node) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := a.Host.Connect(ctx, peer.AddrInfo{ID: b.Host.ID(), Addrs: b.Host.Addrs()}); err != nil {
		t.Fatalf("connect %s -> %s: %v", a.Host.ID(), b.Host.ID(), err)
	}
}

// waitForEvent polls evs until pred matches, failing the test on timeout.
func waitForEvent(t *testing.T, evs <-chan Event, what string, timeout time.Duration, pred func(Event) bool) Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-evs:
			if pred(ev) {
				return ev
			}
		case <-deadline:
			t.Fatalf("timeout waiting for %s", what)
		}
	}
}

func TestGroupChatMessageFlow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nd1 := testNode(t, ctx)
	nd2 := testNode(t, ctx)
	connectNodes(t, nd2, nd1)

	mgr1, err := NewManager(ctx, nd1.Host, nd1, testStore(t))
	if err != nil {
		t.Fatal(err)
	}
	defer mgr1.Close()
	mgr2, err := NewManager(ctx, nd2.Host, nd2, testStore(t))
	if err != nil {
		t.Fatal(err)
	}
	defer mgr2.Close()

	mgr1.SetNick("alice")
	mgr2.SetNick("bob")

	if err := mgr1.Join("lobby", "s3cret passphrase"); err != nil {
		t.Fatalf("mgr1 join: %v", err)
	}
	if err := mgr2.Join("lobby", "s3cret passphrase"); err != nil {
		t.Fatalf("mgr2 join: %v", err)
	}

	// Rooms must be saved.
	if len(mgr1.RoomNames()) != 1 || mgr1.RoomNames()[0] != "lobby" {
		t.Fatalf("mgr1 rooms = %v", mgr1.RoomNames())
	}

	// Wait until both directions are meshed before sending.
	waitForEvent(t, mgr1.Events(), "mgr1 sees mgr2 join", 30*time.Second,
		func(ev Event) bool { return ev.Type == EventJoin && ev.From == nd2.Host.ID() })
	waitForEvent(t, mgr2.Events(), "mgr2 sees mgr1 join", 30*time.Second,
		func(ev Event) bool { return ev.Type == EventJoin && ev.From == nd1.Host.ID() })

	if err := mgr2.Send("lobby", "hello from bob"); err != nil {
		t.Fatalf("send: %v", err)
	}

	got := waitForEvent(t, mgr1.Events(), "message from bob", 30*time.Second,
		func(ev Event) bool { return ev.Type == EventMessage && ev.Msg != nil })
	if got.Msg.Body != "hello from bob" {
		t.Fatalf("body = %q", got.Msg.Body)
	}
	if got.Msg.Nick != "bob" {
		t.Fatalf("nick = %q, want bob", got.Msg.Nick)
	}

	// Both sides should have each other in the member list.
	if cnt := mgr1.MemberCount("lobby"); cnt != 1 {
		t.Fatalf("mgr1 member count = %d, want 1", cnt)
	}
}

func TestWrongPassphraseIsolates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nd1 := testNode(t, ctx)
	nd2 := testNode(t, ctx)
	connectNodes(t, nd2, nd1)

	mgr1, _ := NewManager(ctx, nd1.Host, nd1, testStore(t))
	defer mgr1.Close()
	mgr2, _ := NewManager(ctx, nd2.Host, nd2, testStore(t))
	defer mgr2.Close()

	mgr1.SetNick("alice")
	mgr2.SetNick("mallory")

	if err := mgr1.Join("lobby", "correct passphrase"); err != nil {
		t.Fatal(err)
	}
	// mallory guesses wrong; derives a different topic and key.
	if err := mgr2.Join("lobby", "wrong passphrase"); err != nil {
		t.Fatal(err)
	}

	if err := mgr2.Send("lobby", "can you hear me?"); err != nil {
		t.Fatal(err)
	}

	// mgr1 must NOT receive mallory's message: the topics differ, so the
	// message never even reaches mgr1's topic subscription.
	deadline := time.After(4 * time.Second)
	for {
		select {
		case ev := <-mgr1.Events():
			if ev.Type == EventMessage {
				t.Fatalf("mgr1 received a message from a wrong-passphrase peer: %+v", ev)
			}
			// join/presence events may legitimately arrive; keep waiting.
		case <-deadline:
			return // no message arrived: isolation confirmed
		}
	}
}

func TestSendAppearsInOwnHistory(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nd := testNode(t, ctx)
	mgr, err := NewManager(ctx, nd.Host, nd, testStore(t))
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	mgr.SetNick("alice")
	if err := mgr.Join("solo", "pw"); err != nil {
		t.Fatal(err)
	}

	if err := mgr.Send("solo", "hello myself"); err != nil {
		t.Fatalf("send: %v", err)
	}

	// Sending must echo locally (GossipSub does not deliver to the sender).
	got := waitForEvent(t, mgr.Events(), "own message echo", 10*time.Second,
		func(ev Event) bool { return ev.Type == EventMessage && ev.Msg != nil })
	if !got.Msg.Own {
		t.Fatalf("own message not marked Own: %+v", got.Msg)
	}
	if got.Msg.Body != "hello myself" {
		t.Fatalf("body = %q, want %q", got.Msg.Body, "hello myself")
	}
	if got.Msg.Nick != "alice" {
		t.Fatalf("nick = %q, want alice", got.Msg.Nick)
	}

	// It must also be in the in-session history so re-renders show it.
	found := false
	for _, ms := range mgr.Messages("solo") {
		if ms.ID == got.Msg.ID && ms.Body == "hello myself" && ms.Own {
			found = true
		}
	}
	if !found {
		t.Fatalf("sent message missing from history: %+v", mgr.Messages("solo"))
	}
}

func TestRestoreRejoinsSavedRooms(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nd := testNode(t, ctx)
	st := testStore(t)

	mgr1, err := NewManager(ctx, nd.Host, nd, st)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr1.Join("dev", "pw"); err != nil {
		t.Fatal(err)
	}
	mgr1.Close()

	// Simulate a fresh app start on the same store: new manager, no passphrase.
	mgr2, err := NewManager(ctx, nd.Host, nd, st)
	if err != nil {
		t.Fatal(err)
	}
	defer mgr2.Close()
	if err := mgr2.Restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(mgr2.RoomNames()) != 1 || mgr2.RoomNames()[0] != "dev" {
		t.Fatalf("restored rooms = %v", mgr2.RoomNames())
	}
	if err := mgr2.Send("dev", "back online"); err != nil {
		t.Fatalf("send into restored room: %v", err)
	}
}
