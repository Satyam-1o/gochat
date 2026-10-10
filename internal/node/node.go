// Package node builds and manages the libp2p host: transports, NAT traversal,
// mDNS discovery (LAN) and the PEX client/server (WAN rendezvous).
package node

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/peerstore"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"

	"github.com/multiformats/go-multiaddr"

	"gochat/internal/pex"
)

// DefaultListenAddrStrings are used when none are provided.
var DefaultListenAddrStrings = []string{
	"/ip4/0.0.0.0/tcp/0",
	"/ip4/0.0.0.0/udp/0/quic-v1",
}

// Options configures New.
type Options struct {
	PrivKey     crypto.PrivKey
	ListenAddrs []string
	Bootstrap   []string // rendezvous/relay node multiaddrs (client mode)
	RelayServer bool     // run a circuit relay + PEX server (rendezvous node)
	MDNS        bool     // enable LAN multicast discovery
	MDNSService string
	UserAgent   string
}

// Node wraps the libp2p host and its discovery machinery.
type Node struct {
	ctx    context.Context
	cancel context.CancelFunc
	Host   host.Host

	bootstrap []peer.AddrInfo
	pexSrv    *pex.Server
	pexCli    *pex.Client
	mdns      mdns.Service
	bridge    *pubsubBridge

	dialSem chan struct{}
}

// New creates the libp2p host and starts discovery.
func New(ctx context.Context, opts Options) (*Node, error) {
	if len(opts.ListenAddrs) == 0 {
		opts.ListenAddrs = DefaultListenAddrStrings
	}
	if opts.MDNSService == "" {
		opts.MDNSService = "gochat"
	}
	if opts.UserAgent == "" {
		opts.UserAgent = "gochat"
	}

	bootstrap, err := parseAddrInfos(opts.Bootstrap)
	if err != nil {
		return nil, err
	}

	libp2pOpts := []libp2p.Option{
		libp2p.Identity(opts.PrivKey),
		libp2p.ListenAddrStrings(opts.ListenAddrs...),
		// NOTE: NATPortMap()/UPnP is intentionally omitted — in go-libp2p it
		// breaks the dual-dial (two peers dialing each other simultaneously)
		// case that PEX discovery routinely creates. NAT traversal is instead
		// handled by hole punching + circuit relay.
		libp2p.EnableHolePunching(),
		libp2p.UserAgent(opts.UserAgent),
	}
	if opts.RelayServer {
		libp2pOpts = append(libp2pOpts, libp2p.EnableRelayService(), libp2p.EnableNATService())
	}
	if len(bootstrap) > 0 {
		libp2pOpts = append(libp2pOpts, libp2p.EnableAutoRelayWithStaticRelays(bootstrap))
	}

	h, err := libp2p.New(libp2pOpts...)
	if err != nil {
		return nil, fmt.Errorf("node: create host: %w", err)
	}

	nctx, ncancel := context.WithCancel(ctx)
	n := &Node{
		ctx:       nctx,
		cancel:    ncancel,
		Host:      h,
		bootstrap: bootstrap,
		dialSem:   make(chan struct{}, 16),
	}

	if opts.RelayServer {
		br, err := newPubsubBridge(nctx, h)
		if err != nil {
			n.Close()
			return nil, fmt.Errorf("node: start pubsub bridge: %w", err)
		}
		n.bridge = br
		n.pexSrv = pex.NewServer(h)
		n.pexSrv.SetNamespaceHandler(pex.NamespaceHandler{
			OnJoin:  br.join,
			OnLeave: br.leave,
		})
	}

	if len(bootstrap) > 0 {
		n.pexCli = pex.NewClient(ctx, h, bootstrap[0], n.handlePeerInfo)
		n.pexCli.Start()
	}

	if opts.MDNS {
		n.mdns = mdns.NewMdnsService(h, opts.MDNSService, notifee{onFound: n.handlePeerInfo})
		if err := n.mdns.Start(); err != nil {
			n.Close()
			return nil, fmt.Errorf("node: start mdns: %w", err)
		}
	}

	// Connect to bootstrap rendezvous nodes eagerly so discovery starts fast.
	for _, ai := range bootstrap {
		go n.connectPeer(ai)
	}

	return n, nil
}

// ID returns the node's peer ID.
func (n *Node) ID() peer.ID { return n.Host.ID() }

// Addrs returns the host's current multiaddrs as strings.
func (n *Node) Addrs() []string {
	as := n.Host.Addrs()
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, a.String())
	}
	return out
}

// PeerCount returns the number of currently connected peers.
func (n *Node) PeerCount() int {
	return len(n.Host.Network().Peers())
}

// Advertise registers interest in a namespace (room topic) with the
// rendezvous node so other members can find us. No-op without bootstrap.
func (n *Node) Advertise(namespace string) {
	if n.pexCli != nil {
		n.pexCli.Register(namespace)
	}
}

// StopAdvertise removes interest in a namespace.
func (n *Node) StopAdvertise(namespace string) {
	if n.pexCli != nil {
		n.pexCli.Unregister(namespace)
	}
}

// Close shuts down the host and all discovery services.
func (n *Node) Close() error {
	n.cancel()
	if n.pexCli != nil {
		n.pexCli.Close()
	}
	if n.mdns != nil {
		n.mdns.Close()
	}
	if n.pexSrv != nil {
		n.pexSrv.Close()
	}
	return n.Host.Close()
}

// handlePeerInfo is called by mDNS/PEX when a peer is discovered.
func (n *Node) handlePeerInfo(pi pex.PeerInfo) {
	id, err := peer.Decode(pi.ID)
	if err != nil || id == n.Host.ID() {
		return
	}
	addrs := make([]multiaddr.Multiaddr, 0, len(pi.Addrs))
	for _, s := range pi.Addrs {
		if a, err := multiaddr.NewMultiaddr(s); err == nil {
			addrs = append(addrs, a)
		}
	}
	if len(addrs) == 0 {
		return
	}
	n.Host.Peerstore().AddAddrs(id, addrs, peerstore.TempAddrTTL)
	n.connectPeer(peer.AddrInfo{ID: id, Addrs: addrs})
}

func (n *Node) connectPeer(ai peer.AddrInfo) {
	if ai.ID == n.Host.ID() {
		return
	}
	select {
	case n.dialSem <- struct{}{}:
		defer func() { <-n.dialSem }()
	case <-n.ctx.Done():
		return
	}

	if n.Host.Network().Connectedness(ai.ID) == network.Connected {
		return
	}
	ctx, cancel := context.WithTimeout(n.ctx, 15*time.Second)
	defer cancel()
	if err := n.Host.Connect(ctx, ai); err != nil {
		// Not a hard failure: the rendezvous bridge handles message delivery,
		// and direct dials are only an optimization for LAN/direct links.
		log.Printf("node: direct dial %s failed (bridge still routes): %v", ai.ID, err)
	}
}

type notifee struct{ onFound func(pex.PeerInfo) }

func (n notifee) HandlePeerFound(ai peer.AddrInfo) {
	addrs := make([]string, 0, len(ai.Addrs))
	for _, a := range ai.Addrs {
		addrs = append(addrs, a.String())
	}
	n.onFound(pex.PeerInfo{ID: ai.ID.String(), Addrs: addrs})
}

// parseAddrInfos converts multiaddr strings (with /p2p/<peerID> suffix) into
// peer.AddrInfo values.
func parseAddrInfos(addrs []string) ([]peer.AddrInfo, error) {
	var out []peer.AddrInfo
	for _, s := range addrs {
		maddr, err := multiaddr.NewMultiaddr(s)
		if err != nil {
			return nil, fmt.Errorf("node: bad multiaddr %q: %w", s, err)
		}
		ai, err := peer.AddrInfoFromP2pAddr(maddr)
		if err != nil {
			return nil, fmt.Errorf("node: missing peer ID in %q: %w", s, err)
		}
		out = append(out, *ai)
	}
	return out, nil
}
