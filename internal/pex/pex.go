// Package pex implements a tiny peer-exchange protocol ("PEX") used for
// internet-wide room discovery. A rendezvous node runs the Server; chat
// clients run the Client. Clients register interest in a namespace (the
// room's unguessable topic hash) and the server introduces them to each
// other by sharing multiaddrs. Actual message traffic flows peer-to-peer —
// the server never sees or decodes it.
package pex

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
)

// ProtoID is the libp2p stream protocol identifier.
const ProtoID = "/gochat/pex/1.0.0"

// MaxMessage caps a single framed message at 1 MiB.
const MaxMessage = 1 << 20

// PeerInfo carries everything another client needs to dial a peer.
type PeerInfo struct {
	ID    string   `json:"id"`
	Addrs []string `json:"addrs,omitempty"`
}

// Msg is sent client -> server.
type Msg struct {
	Type      string   `json:"type"` // "register" | "unregister"
	Namespace string   `json:"namespace"`
	Addrs     []string `json:"addrs,omitempty"`
}

// Event is sent server -> client.
type Event struct {
	Type      string     `json:"type"` // "peers" | "peer_joined" | "peer_left"
	Namespace string     `json:"namespace,omitempty"`
	Peers     []PeerInfo `json:"peers,omitempty"`
	Peer      *PeerInfo  `json:"peer,omitempty"`
}

var errTooLarge = errors.New("pex: message exceeds size limit")

func writeMsg(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(data) > MaxMessage {
		return errTooLarge
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func readMsg(r io.Reader, v any) error {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > MaxMessage {
		return errTooLarge
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(r, data); err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// ---------------------------------------------------------------------------
// Server (run by the rendezvous node)
// ---------------------------------------------------------------------------

// NamespaceHandler is invoked when a namespace's membership transitions
// between 0 and 1 registered clients. The rendezvous node uses it to bridge
// the room's pubsub topic (it only ever sees hashed topics + ciphertext).
type NamespaceHandler struct {
	OnJoin  func(namespace string)
	OnLeave func(namespace string)
}

// Server tracks which namespaces each connected client is interested in and
// introduces peers to one another.
type Server struct {
	h host.Host

	mu         sync.Mutex
	conns      map[peer.ID]*serverConn
	namespaces map[string]map[peer.ID]struct{}
	handler    NamespaceHandler
}

// SetNamespaceHandler installs the topic-bridge hooks (rendezvous node only).
func (s *Server) SetNamespaceHandler(h NamespaceHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handler = h
}

type serverConn struct {
	stream     network.Stream
	peer       peer.ID
	info       PeerInfo
	namespaces map[string]struct{}
}

// NewServer registers the PEX stream handler on h. Call Close on shutdown.
func NewServer(h host.Host) *Server {
	s := &Server{
		h:          h,
		conns:      make(map[peer.ID]*serverConn),
		namespaces: make(map[string]map[peer.ID]struct{}),
	}
	h.SetStreamHandler(ProtoID, s.handleStream)
	return s
}

func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		c.stream.Close()
	}
	s.conns = nil
	s.namespaces = nil
	return nil
}

func (s *Server) handleStream(stream network.Stream) {
	defer stream.Close()

	pid := stream.Conn().RemotePeer()

	s.mu.Lock()
	if prev := s.conns[pid]; prev != nil {
		prev.stream.Close()
	}
	c := &serverConn{
		stream:     stream,
		peer:       pid,
		info:       PeerInfo{ID: pid.String()},
		namespaces: make(map[string]struct{}),
	}
	s.conns[pid] = c
	s.mu.Unlock()

	defer func() {
		s.dropConn(c)
	}()

	for {
		var msg Msg
		if err := readMsg(stream, &msg); err != nil {
			return
		}
		s.mu.Lock()
		switch msg.Type {
		case "register":
			c.info.Addrs = dedupStrings(append(c.info.Addrs, msg.Addrs...))
			// The server observes the client's connection address; it is the
			// most reliable dialable address in many NAT setups.
			if ra := stream.Conn().RemoteMultiaddr(); ra != nil {
				c.info.Addrs = dedupStrings(append(c.info.Addrs, ra.String()))
			}
			c.namespaces[msg.Namespace] = struct{}{}
			s.addNamespace(msg.Namespace, c)
		case "unregister":
			delete(c.namespaces, msg.Namespace)
			s.removeNamespace(msg.Namespace, c.peer)
		}
		s.mu.Unlock()
	}
}

func (s *Server) addNamespace(ns string, c *serverConn) {
	m, ok := s.namespaces[ns]
	if !ok {
		m = make(map[peer.ID]struct{})
		s.namespaces[ns] = m
		if h := s.handler; h.OnJoin != nil {
			h.OnJoin(ns)
		}
	}
	m[c.peer] = struct{}{}

	// Reply with everyone currently in the namespace (excluding self).
	var peers []PeerInfo
	for p := range m {
		if p == c.peer {
			continue
		}
		if other := s.conns[p]; other != nil {
			peers = append(peers, other.info)
		}
	}
	log.Printf("pex: register ns=%q self=%s addrs=%v peers=%v", ns, c.peer, c.info.Addrs, peerIDs(peers))
	if len(peers) > 0 {
		writeMsg(c.stream, Event{Type: "peers", Namespace: ns, Peers: peers})
	}

	// Tell everyone else about the newcomer.
	for p := range m {
		if p == c.peer {
			continue
		}
		if other := s.conns[p]; other != nil {
			s.writeTo(other, Event{Type: "peer_joined", Namespace: ns, Peer: &c.info})
		}
	}
}

func (s *Server) writeTo(c *serverConn, ev Event) {
	_ = c.stream.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := writeMsg(c.stream, ev); err != nil {
		c.stream.Close()
	}
}

func (s *Server) removeNamespace(ns string, pid peer.ID) {
	if m, ok := s.namespaces[ns]; ok {
		delete(m, pid)
		if len(m) == 0 {
			delete(s.namespaces, ns)
			if h := s.handler; h.OnLeave != nil {
				h.OnLeave(ns)
			}
		}
	}
	ev := Event{Type: "peer_left", Namespace: ns, Peer: &PeerInfo{ID: pid.String()}}
	for p, other := range s.conns {
		if p == pid {
			continue
		}
		if _, ok := other.namespaces[ns]; ok {
			s.writeTo(other, ev)
		}
	}
}

func (s *Server) dropConn(c *serverConn) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if got, ok := s.conns[c.peer]; ok && got == c {
		delete(s.conns, c.peer)
	}
	for ns := range c.namespaces {
		s.removeNamespace(ns, c.peer)
	}
}

// ---------------------------------------------------------------------------
// Client (run by gochat users)
// ---------------------------------------------------------------------------

// Client keeps a PEX stream open to one rendezvous node, advertises interest
// in a set of namespaces, and reports discovered peers via OnPeer.
type Client struct {
	h         host.Host
	server    peer.AddrInfo
	onPeer    func(PeerInfo)
	lifecycle context.Context
	cancel    context.CancelFunc

	mu        sync.Mutex
	interests map[string]struct{}
	stream    network.Stream
	closed    bool
}

// NewClient creates a PEX client for the given rendezvous node. server must
// carry at least one dialable multiaddr. onPeer is invoked whenever the
// server introduces a new peer.
func NewClient(ctx context.Context, h host.Host, server peer.AddrInfo, onPeer func(PeerInfo)) *Client {
	cctx, cancel := context.WithCancel(ctx)
	return &Client{
		h:         h,
		server:    server,
		onPeer:    onPeer,
		lifecycle: cctx,
		cancel:    cancel,
		interests: make(map[string]struct{}),
	}
}

// Register advertises interest in a namespace (room topic).
func (c *Client) Register(namespace string) {
	c.mu.Lock()
	c.interests[namespace] = struct{}{}
	s := c.stream
	c.mu.Unlock()
	if s != nil {
		_ = s.SetWriteDeadline(time.Now().Add(10 * time.Second))
		_ = writeMsg(s, Msg{Type: "register", Namespace: namespace, Addrs: c.selfAddrs()})
	}
}

// Unregister stops advertising interest in a namespace.
func (c *Client) Unregister(namespace string) {
	c.mu.Lock()
	delete(c.interests, namespace)
	s := c.stream
	c.mu.Unlock()
	if s != nil {
		_ = s.SetWriteDeadline(time.Now().Add(10 * time.Second))
		_ = writeMsg(s, Msg{Type: "unregister", Namespace: namespace})
	}
}

// Start runs the reconnect loop until Close is called.
func (c *Client) Start() {
	go c.run()
}

// Close tears down the client.
func (c *Client) Close() {
	c.mu.Lock()
	c.closed = true
	if c.stream != nil {
		c.stream.Close()
	}
	c.mu.Unlock()
	c.cancel()
}

func (c *Client) selfAddrs() []string {
	addrs := c.h.Addrs()
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.String())
	}
	return out
}

func (c *Client) run() {
	const maxBackoff = 30 * time.Second
	backoff := 2 * time.Second
	for {
		if c.isClosed() {
			return
		}
		// Wait until we can connect to the rendezvous node.
		if err := c.establish(); err != nil {
			select {
			case <-c.lifecycle.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < maxBackoff {
				backoff *= 2
			}
			continue
		}
		backoff = 2 * time.Second // reset on success
		// Block until the stream dies or we shut down.
		select {
		case <-c.lifecycle.Done():
			return
		case <-c.streamClosed():
		}
	}
}

// streamClosed returns a channel closed when the current stream fails.
// Called only from run(), so stream is guaranteed non-nil.
func (c *Client) streamClosed() <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.mu.Lock()
		s := c.stream
		c.mu.Unlock()
		if s == nil {
			return
		}
		// Read until error; the reader goroutine also handles events.
		var ev Event
		for readMsg(s, &ev) == nil {
			c.handleEvent(ev)
		}
	}()
	return done
}

func (c *Client) handleEvent(ev Event) {
	switch ev.Type {
	case "peers":
		for _, pi := range ev.Peers {
			c.onPeer(pi)
		}
	case "peer_joined":
		if ev.Peer != nil {
			c.onPeer(*ev.Peer)
		}
	}
}

func (c *Client) establish() error {
	ctx, cancel := context.WithTimeout(c.lifecycle, 15*time.Second)
	defer cancel()

	// Make sure we are connected to the rendezvous node.
	if err := c.h.Connect(ctx, c.server); err != nil {
		return fmt.Errorf("px: connect rendezvous: %w", err)
	}
	s, err := c.h.NewStream(ctx, c.server.ID, ProtoID)
	if err != nil {
		return fmt.Errorf("px: open stream: %w", err)
	}
	_ = s.SetReadDeadline(time.Time{}) // reader goroutine manages lifetime

	c.mu.Lock()
	c.stream = s
	interests := make([]string, 0, len(c.interests))
	for ns := range c.interests {
		interests = append(interests, ns)
	}
	self := c.selfAddrs()
	c.mu.Unlock()

	// Re-register all current interests on a fresh stream (server state
	// does not survive a disconnect).
	_ = s.SetWriteDeadline(time.Now().Add(15 * time.Second))
	for _, ns := range interests {
		if err := writeMsg(s, Msg{Type: "register", Namespace: ns, Addrs: self}); err != nil {
			s.Close()
			return fmt.Errorf("px: register: %w", err)
		}
	}
	return nil
}

func (c *Client) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func peerIDs(ps []PeerInfo) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.ID)
	}
	return out
}

func dedupStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
