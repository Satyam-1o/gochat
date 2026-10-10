package room

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p/core/network"

	"gochat/internal/identity"
	"gochat/internal/node"
)

// testServerNode creates a rendezvous/relay node (PEX server) on loopback.
func testServerNode(t *testing.T, ctx context.Context) *node.Node {
	t.Helper()
	priv, err := identity.LoadOrCreate(filepath.Join(t.TempDir(), "srv.key"))
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	nd, err := node.New(ctx, node.Options{
		PrivKey:     priv,
		ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"},
		RelayServer: true,
		MDNS:        false,
	})
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	t.Cleanup(func() { nd.Close() })
	return nd
}

// testClientNode creates a client node that uses the given rendezvous server
// purely for discovery (no direct connection to the other client beforehand).
func testClientNode(t *testing.T, ctx context.Context, srv *node.Node) *node.Node {
	t.Helper()
	priv, err := identity.LoadOrCreate(filepath.Join(t.TempDir(), "client.key"))
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	bootstrap := srv.Addrs()[0] + "/p2p/" + srv.ID().String()
	nd, err := node.New(ctx, node.Options{
		PrivKey:     priv,
		ListenAddrs: []string{"/ip4/127.0.0.1/tcp/0"},
		Bootstrap:   []string{bootstrap},
		MDNS:        false,
	})
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	t.Cleanup(func() { nd.Close() })
	return nd
}

// TestEndToEndViaRendezvous wires the full internet path: two clients that
// have never met discover each other through the rendezvous node (PEX), which
// bridges their encrypted messages. The clients need not be directly
// reachable — the rendezvous node relays (it only sees hashed topics and
// ciphertext).
func TestEndToEndViaRendezvous(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	srv := testServerNode(t, ctx)
	nd1 := testClientNode(t, ctx, srv)
	nd2 := testClientNode(t, ctx, srv)

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

	if err := mgr1.Join("rendezvous-room", "shared secret"); err != nil {
		t.Fatalf("mgr1 join: %v", err)
	}
	if err := mgr2.Join("rendezvous-room", "shared secret"); err != nil {
		t.Fatalf("mgr2 join: %v", err)
	}

	// The clients have no direct link yet — they must find each other via PEX.
	if nd1.Host.Network().Connectedness(nd2.Host.ID()) == network.Connected {
		t.Fatalf("clients should not be connected before PEX discovery")
	}

	// Wait for mutual membership through PEX + gossipsub.
	waitForEvent(t, mgr1.Events(), "mgr1 sees mgr2 (via rendezvous)", 60*time.Second,
		func(ev Event) bool { return ev.Type == EventJoin && ev.From == nd2.Host.ID() })
	waitForEvent(t, mgr2.Events(), "mgr2 sees mgr1 (via rendezvous)", 60*time.Second,
		func(ev Event) bool { return ev.Type == EventJoin && ev.From == nd1.Host.ID() })

	if err := mgr2.Send("rendezvous-room", "hi from across the internet"); err != nil {
		t.Fatalf("send: %v", err)
	}
	got := waitForEvent(t, mgr1.Events(), "message via rendezvous", 60*time.Second,
		func(ev Event) bool { return ev.Type == EventMessage && ev.Msg != nil })
	if got.Msg.Body != "hi from across the internet" {
		t.Fatalf("body = %q", got.Msg.Body)
	}
	if got.Msg.Nick != "bob" {
		t.Fatalf("nick = %q, want bob", got.Msg.Nick)
	}
}
