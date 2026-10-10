package pex

import (
	"context"
	"testing"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
)

func newHost(t *testing.T) host.Host {
	t.Helper()
	h, err := libp2p.New(libp2p.ListenAddrStrings("/ip4/127.0.0.1/tcp/0"))
	if err != nil {
		t.Fatalf("create host: %v", err)
	}
	t.Cleanup(func() { h.Close() })
	return h
}

func connectTestHosts(t *testing.T, a, b host.Host) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := a.Connect(ctx, peer.AddrInfo{ID: b.ID(), Addrs: b.Addrs()}); err != nil {
		t.Fatalf("connect %s -> %s: %v", a.ID(), b.ID(), err)
	}
}

func newClient(t *testing.T, h host.Host, srv host.Host, onPeer func(PeerInfo)) *Client {
	t.Helper()
	c := NewClient(context.Background(), h, peer.AddrInfo{ID: srv.ID(), Addrs: srv.Addrs()}, onPeer)
	t.Cleanup(c.Close)
	return c
}

func TestRegisterUnregister(t *testing.T) {
	srvHost := newHost(t)
	srv := NewServer(srvHost)
	t.Cleanup(func() { srv.Close() })

	cHost := newHost(t)
	c := NewClient(context.Background(), cHost, peer.AddrInfo{ID: srvHost.ID(), Addrs: srvHost.Addrs()}, func(PeerInfo) {})
	c.Register("ns-a")
	c.Register("ns-b")
	c.Start()

	deadline := time.Now().Add(10 * time.Second)
	for {
		srv.mu.Lock()
		hasAB := len(srv.namespaces["ns-a"]) == 1
		hasB := len(srv.namespaces["ns-b"]) == 1
		srv.mu.Unlock()
		if hasAB && hasB {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("namespaces not registered in time: ns-a=%v ns-b=%v", hasAB, hasB)
		}
		time.Sleep(50 * time.Millisecond)
	}

	c.Unregister("ns-a")
	deadline = time.Now().Add(10 * time.Second)
	for {
		srv.mu.Lock()
		hasA := len(srv.namespaces["ns-a"])
		srv.mu.Unlock()
		if hasA == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ns-a still has %d members after unregister", hasA)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestPeerDiscovery(t *testing.T) {
	srvHost := newHost(t)
	srv := NewServer(srvHost)
	t.Cleanup(func() { srv.Close() })

	// c1 joins first; gets an empty peer list but is waiting for newcomers.
	c1Host := newHost(t)
	c1 := NewClient(context.Background(), c1Host, peer.AddrInfo{ID: srvHost.ID(), Addrs: srvHost.Addrs()}, func(PeerInfo) {})
	connectTestHosts(t, c1Host, srvHost)
	c1.Register("ns-x")
	c1.Start()

	// c2 joins later and must discover c1.
	found := make(chan PeerInfo, 4)
	c2Host := newHost(t)
	c2 := NewClient(context.Background(), c2Host, peer.AddrInfo{ID: srvHost.ID(), Addrs: srvHost.Addrs()}, func(pi PeerInfo) { found <- pi })
	connectTestHosts(t, c2Host, srvHost)
	c2.Register("ns-x")
	c2.Start()

	select {
	case pi := <-found:
		if pi.ID != c1Host.ID().String() {
			t.Fatalf("discovered %s, want %s", pi.ID, c1Host.ID())
		}
		if len(pi.Addrs) == 0 {
			t.Fatal("discovered peer has no addresses")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("timeout: c2 never discovered c1")
	}
}

func TestDisconnectCleansUp(t *testing.T) {
	srvHost := newHost(t)
	srv := NewServer(srvHost)
	t.Cleanup(func() { srv.Close() })

	cHost := newHost(t)
	c := NewClient(context.Background(), cHost, peer.AddrInfo{ID: srvHost.ID(), Addrs: srvHost.Addrs()}, func(PeerInfo) {})
	c.Register("ns-z")
	c.Start()

	deadline := time.Now().Add(10 * time.Second)
	for {
		srv.mu.Lock()
		registered := len(srv.namespaces["ns-z"]) == 1
		srv.mu.Unlock()
		if registered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("namespace never registered")
		}
		time.Sleep(50 * time.Millisecond)
	}

	cHost.Close() // drop the connection; server must clean up

	deadline = time.Now().Add(15 * time.Second)
	for {
		srv.mu.Lock()
		inNs := len(srv.namespaces["ns-z"])
		hasConn := srv.conns[cHost.ID()] != nil
		srv.mu.Unlock()
		if inNs == 0 && !hasConn {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server still holds state after disconnect: ns=%d conn=%v", inNs, hasConn)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
