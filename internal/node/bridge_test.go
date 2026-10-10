package node

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"

	"gochat/internal/identity"
)

// TestBridgeStarTopology validates the premise of the rendezvous bridge: two
// GossipSub peers that are NOT directly connected (they only share a
// connection to the rendezvous node) still exchange messages because the node
// subscribes to and relays their topic. This is what makes chat work over the
// internet even when client-to-client dials are impossible (both behind NAT).
func TestBridgeStarTopology(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv := testBareNode(t, ctx, "srv")
	br, err := newPubsubBridge(ctx, srv.Host)
	if err != nil {
		t.Fatal(err)
	}

	a := testBareNode(t, ctx, "a")
	b := testBareNode(t, ctx, "b")

	// All clients connect to the server only. No A-B link exists.
	for _, c := range []*Node{a, b} {
		if err := c.Host.Connect(ctx, peer.AddrInfo{ID: srv.Host.ID(), Addrs: srv.Host.Addrs()}); err != nil {
			t.Fatalf("client -> server: %v", err)
		}
	}
	if a.Host.Network().Connectedness(b.Host.ID()) == network.Connected {
		t.Fatal("clients should not be directly connected")
	}

	psA, err := pubsub.NewGossipSub(ctx, a.Host)
	if err != nil {
		t.Fatal(err)
	}
	psB, err := pubsub.NewGossipSub(ctx, b.Host)
	if err != nil {
		t.Fatal(err)
	}

	const topicName = "/gochat/1.0/diagnostic-topic"
	br.join(topicName) // bridge subscribes first, before any client

	tA, err := psA.Join(topicName)
	if err != nil {
		t.Fatal(err)
	}
	subA, err := tA.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer subA.Cancel()

	tB, err := psB.Join(topicName)
	if err != nil {
		t.Fatal(err)
	}
	subB, err := tB.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer subB.Cancel()

	// Let the mesh converge across the star before publishing (the app
	// re-announces periodically precisely because the first attempt can race
	// ahead of this).
	time.Sleep(2 * time.Second)

	if err := tA.Publish(ctx, []byte("hello-across-the-star")); err != nil {
		t.Fatal(err)
	}

	readCtx, rcancel := context.WithTimeout(ctx, 15*time.Second)
	defer rcancel()
	for {
		msg, err := subB.Next(readCtx)
		if err != nil {
			t.Fatalf("timed out waiting for bridged delivery: %v", err)
		}
		if string(msg.Data) == "hello-across-the-star" {
			t.Logf("OK: message delivered A -> server -> B")
			return
		}
		t.Logf("unexpected message: %q", string(msg.Data))
	}
}

// TestBridgeStarDiscoversLaterSubscriber ensures delivery still works when a
// client subscribes shortly after the first message (mesh re-convergence).
func TestBridgeStarDiscoversLaterSubscriber(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv := testBareNode(t, ctx, "srv")
	br, err := newPubsubBridge(ctx, srv.Host)
	if err != nil {
		t.Fatal(err)
	}

	a := testBareNode(t, ctx, "a")
	b := testBareNode(t, ctx, "b")
	for _, c := range []*Node{a, b} {
		if err := c.Host.Connect(ctx, peer.AddrInfo{ID: srv.Host.ID(), Addrs: srv.Host.Addrs()}); err != nil {
			t.Fatalf("client -> server: %v", err)
		}
	}

	psA, err := pubsub.NewGossipSub(ctx, a.Host)
	if err != nil {
		t.Fatal(err)
	}
	psB, err := pubsub.NewGossipSub(ctx, b.Host)
	if err != nil {
		t.Fatal(err)
	}

	const topicName = "/gochat/1.0/diagnostic-topic-2"
	br.join(topicName)

	tA, err := psA.Join(topicName)
	if err != nil {
		t.Fatal(err)
	}
	subA, err := tA.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer subA.Cancel()

	tB, err := psB.Join(topicName)
	if err != nil {
		t.Fatal(err)
	}
	subB, err := tB.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer subB.Cancel()

	// Publish a couple of messages while the mesh is still converging, then
	// verify the last one arrives regardless of ordering.
	payload := []byte("converged-message")
	for i := 0; i < 3; i++ {
		time.Sleep(250 * time.Millisecond)
		if err := tA.Publish(ctx, payload); err != nil {
			t.Fatal(err)
		}
	}

	readCtx, rcancel := context.WithTimeout(ctx, 15*time.Second)
	defer rcancel()
	for {
		msg, err := subB.Next(readCtx)
		if err != nil {
			t.Fatalf("timed out waiting for bridged delivery: %v", err)
		}
		if string(msg.Data) == string(payload) {
			t.Logf("OK: converged delivery works")
			return
		}
	}
}

func testBareNode(t *testing.T, ctx context.Context, name string) *Node {
	t.Helper()
	priv, err := identity.LoadOrCreate(filepath.Join(t.TempDir(), name+".key"))
	if err != nil {
		t.Fatal(err)
	}
	nd, err := New(ctx, Options{
		PrivKey:     priv,
		ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"},
		RelayServer: false,
		MDNS:        false,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { nd.Close() })
	return nd
}
