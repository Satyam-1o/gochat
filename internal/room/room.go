// Package room implements group chat rooms over libp2p GossipSub, with
// passphrase-derived topics and end-to-end encryption of every message.
//
// Rooms are persistent (saved to disk) but messages are ephemeral: history
// lives only in memory for the current session, so restarting the app clears
// the chat while remembering the rooms themselves.
package room

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"

	"github.com/Satyam-1o/gochat/internal/crypto"
	"github.com/Satyam-1o/gochat/internal/node"
	"github.com/Satyam-1o/gochat/internal/store"
)

// EventType enumerates the events a Manager emits to the UI.
type EventType int

const (
	// EventMessage is a received chat message.
	EventMessage EventType = iota
	// EventJoin is a peer announcing itself via its first presence message.
	EventJoin
	// EventLeave is a directly-meshed peer disappearing.
	EventLeave
	// EventPresence is a peer announcing a nickname change.
	EventPresence
	// EventRoomJoined fires after we successfully joined a room.
	EventRoomJoined
	// EventRoomLeft fires after we left a room.
	EventRoomLeft
	// EventError reports a non-fatal error to the UI.
	EventError
)

// Event is delivered to the UI via Manager.Events().
type Event struct {
	Type EventType
	Room string
	From peer.ID
	Nick string
	Msg  *Message
	Err  error
}

// Message is a single chat message (in-session, never persisted).
type Message struct {
	ID   string
	Room string
	Nick string
	Body string
	TS   int64
	Own  bool // true for messages we sent ourselves (local echo)
}

// wirePayload is the encrypted plaintext published to pubsub.
type wirePayload struct {
	Kind string `json:"kind"` // "msg" | "presence"
	ID   string `json:"id,omitempty"`
	Nick string `json:"nick,omitempty"`
	Body string `json:"body,omitempty"`
	TS   int64  `json:"ts,omitempty"`
}

const (
	maxHistoryPerRoom = 500
	maxSeenMessages   = 4096
)

// Room is an active subscription to one topic.
type Room struct {
	Name  string
	Topic string
	key   []byte

	topic  *pubsub.Topic
	sub    *pubsub.Subscription
	evh    *pubsub.TopicEventHandler
	cancel context.CancelFunc

	mu      sync.Mutex
	members map[peer.ID]string // peer ID -> known nick ("" if unknown)
}

// Members returns a copy of the member map keyed by peer ID.
func (r *Room) Members() map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]string, len(r.members))
	for id, nick := range r.members {
		out[id.String()] = nick
	}
	return out
}

func (r *Room) memberCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.members)
}

func (r *Room) setNick(p peer.ID, nick string) {
	if nick == "" {
		return
	}
	r.mu.Lock()
	r.members[p] = nick
	r.mu.Unlock()
}

func (r *Room) removeMember(p peer.ID) {
	r.mu.Lock()
	delete(r.members, p)
	r.mu.Unlock()
}

// Manager owns all rooms and the pubsub instance backing them.
type Manager struct {
	ctx    context.Context
	cancel context.CancelFunc

	host  host.Host
	ps    *pubsub.PubSub
	node  *node.Node
	store *store.Store

	mu        sync.Mutex
	rooms     map[string]*Room // by topic
	byName    map[string]*Room // by display name
	history   map[string][]Message
	seen      map[string]struct{}
	seenOrder []string
	events    chan Event
}

// NewManager creates the pubsub instance and a Manager. It is cheap to create
// and does not connect or join anything until Join/Restore is called.
func NewManager(ctx context.Context, h host.Host, nd *node.Node, st *store.Store) (*Manager, error) {
	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		return nil, fmt.Errorf("room: pubsub: %w", err)
	}
	mctx, mcancel := context.WithCancel(ctx)
	return &Manager{
		ctx:     mctx,
		cancel:  mcancel,
		host:    h,
		ps:      ps,
		node:    nd,
		store:   st,
		rooms:   make(map[string]*Room),
		byName:  make(map[string]*Room),
		history: make(map[string][]Message),
		seen:    make(map[string]struct{}),
		events:  make(chan Event, 256),
	}, nil
}

// Events returns the channel of UI events. The channel is never closed.
func (m *Manager) Events() <-chan Event { return m.events }

// Nick is the effective display name, falling back to a short peer ID.
func (m *Manager) Nick() string {
	cfg := m.store.LoadConfig()
	if cfg.DisplayName != "" {
		return cfg.DisplayName
	}
	return shortID(m.host.ID())
}

// SetNick updates the display name, persists it, and announces it in every
// room we are currently in.
func (m *Manager) SetNick(nick string) {
	cfg := m.store.LoadConfig()
	cfg.DisplayName = nick
	_ = m.store.SaveConfig(cfg)

	m.mu.Lock()
	var rooms []*Room
	for _, r := range m.rooms {
		rooms = append(rooms, r)
	}
	m.mu.Unlock()

	for _, r := range rooms {
		m.publishPresence(r)
	}
}

// Join joins (or re-joins) a room by name and passphrase. The topic is derived
// from the passphrase, and the resulting room is persisted for auto-reconnect.
func (m *Manager) Join(roomName, passphrase string) error {
	roomName = trimRoomName(roomName)
	if roomName == "" || passphrase == "" {
		return fmt.Errorf("room: join needs a name and a passphrase")
	}
	key := crypto.DeriveRoomKey(passphrase)
	topic := crypto.RoomTopic(roomName, key)

	m.mu.Lock()
	if prev, ok := m.byName[roomName]; ok && prev.Topic != topic {
		// Same display name, different passphrase: drop the stale room first.
		m.mu.Unlock()
		if err := m.Leave(roomName); err != nil {
			return err
		}
		m.mu.Lock()
	}
	m.mu.Unlock()

	if err := m.joinTopic(roomName, topic, key); err != nil {
		return err
	}
	_, err := m.store.UpsertRoom(store.Room{
		Name:      roomName,
		Topic:     topic,
		Key:       base64.StdEncoding.EncodeToString(key),
		CreatedAt: time.Now(),
	})
	return err
}

// JoinSaved re-joins a previously saved room from its stored key and topic
// without the passphrase.
func (m *Manager) JoinSaved(roomName, topic string, key []byte) error {
	roomName = trimRoomName(roomName)
	if roomName == "" {
		return fmt.Errorf("room: empty room name")
	}
	return m.joinTopic(roomName, topic, key)
}

// joinTopic subscribes to topic and wires up the message/peer readers.
func (m *Manager) joinTopic(name, topic string, key []byte) error {
	m.mu.Lock()
	if existing, ok := m.rooms[topic]; ok {
		existingName := existing.Name
		m.mu.Unlock()
		if existingName != name {
			return fmt.Errorf("room: topic collision for %q", name)
		}
		return nil
	}
	m.mu.Unlock()

	t, err := m.ps.Join(topic)
	if err != nil {
		return fmt.Errorf("room: join topic: %w", err)
	}
	sub, err := t.Subscribe()
	if err != nil {
		return fmt.Errorf("room: subscribe: %w", err)
	}
	evh, err := t.EventHandler()
	if err != nil {
		sub.Cancel()
		return fmt.Errorf("room: event handler: %w", err)
	}

	rctx, rcancel := context.WithCancel(m.ctx)
	r := &Room{
		Name:    name,
		Topic:   topic,
		key:     key,
		topic:   t,
		sub:     sub,
		evh:     evh,
		cancel:  rcancel,
		members: make(map[peer.ID]string),
	}

	m.mu.Lock()
	m.rooms[topic] = r
	m.byName[name] = r
	m.mu.Unlock()

	m.node.Advertise(topic)
	m.publishPresence(r)
	m.emit(Event{Type: EventRoomJoined, Room: name})

	go m.readMessages(r, rctx)
	go m.readPeerEvents(r, rctx)
	go m.presenceLoop(r, rctx)
	return nil
}

// Leave leaves the room by display name and forgets it.
func (m *Manager) Leave(name string) error {
	name = trimRoomName(name)
	m.mu.Lock()
	r, ok := m.byName[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("room: not in room %q", name)
	}
	delete(m.byName, name)
	delete(m.rooms, r.Topic)
	r.cancel()
	m.mu.Unlock()

	r.sub.Cancel()
	r.evh.Cancel()
	m.node.StopAdvertise(r.Topic)

	// Best-effort removal from the store; failure is not fatal.
	_ = m.store.DeleteRoom(name)
	m.emit(Event{Type: EventRoomLeft, Room: name})
	return nil
}

// Send publishes a message to the given room. Only group members can decrypt it.
func (m *Manager) Send(name, body string) error {
	m.mu.Lock()
	r, ok := m.byName[name]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("not in room %q", name)
	}
	nick := m.Nick()
	p := wirePayload{Kind: "msg", ID: uuid.NewString(), Nick: nick, Body: body, TS: time.Now().Unix()}
	plain, err := json.Marshal(p)
	if err != nil {
		return err
	}
	env, err := m.seal(r, plain)
	if err != nil {
		return err
	}
	if err := r.topic.Publish(m.ctx, env); err != nil {
		return err
	}

	// Local echo: show our own message immediately. GossipSub never delivers a
	// message back to its sender, and readMessages skips our own peer ID, so
	// this is the only place our sent messages enter the UI.
	m.saw(p.ID)
	ms := &Message{ID: p.ID, Room: r.Name, Nick: nick, Body: body, TS: p.TS, Own: true}
	m.appendHistory(r.Name, ms)
	m.emit(Event{Type: EventMessage, Room: r.Name, From: m.host.ID(), Nick: nick, Msg: ms})
	return nil
}

// Restore re-joins every room saved on disk (auto-reconnect on startup).
func (m *Manager) Restore() error {
	rooms, err := m.store.LoadRooms()
	if err != nil {
		return fmt.Errorf("room: restore: %w", err)
	}
	for _, sr := range rooms {
		key, err := sr.KeyBytes()
		if err != nil {
			log.Printf("room: skip %q: bad key: %v", sr.Name, err)
			continue
		}
		if err := m.JoinSaved(sr.Name, sr.Topic, key); err != nil {
			log.Printf("room: skip %q: %v", sr.Name, err)
		}
	}
	return nil
}

// RoomNames returns the display names of all active rooms, sorted.
func (m *Manager) RoomNames() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	names := make([]string, 0, len(m.byName))
	for n := range m.byName {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Messages returns the in-session history for a room.
func (m *Manager) Messages(name string) []Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Message(nil), m.history[name]...)
}

// MemberCount returns how many peers are currently in a room.
func (m *Manager) MemberCount(name string) int {
	m.mu.Lock()
	r := m.byName[name]
	m.mu.Unlock()
	if r == nil {
		return 0
	}
	return r.memberCount()
}

// MembersMap returns the member map (peer ID -> nick) for a room.
func (m *Manager) MembersMap(name string) map[string]string {
	m.mu.Lock()
	r := m.byName[name]
	m.mu.Unlock()
	if r == nil {
		return nil
	}
	return r.Members()
}

// Close cancels all subscriptions and shuts down the manager.
func (m *Manager) Close() {
	m.cancel()
}

// ---------------------------------------------------------------------------
// internals
// ---------------------------------------------------------------------------

// aad builds the AEAD additional data binding a message to its sender + topic.
func aad(topic string, p peer.ID) []byte {
	out := make([]byte, 0, len(p)+len(topic))
	out = append(out, []byte(p)...)
	out = append(out, topic...)
	return out
}

// seal encrypts a payload for room r (AAD = our peer ID + topic).
func (m *Manager) seal(r *Room, plain []byte) ([]byte, error) {
	return crypto.Encrypt(r.key, aad(r.Topic, m.host.ID()), plain)
}

// open decrypts a payload sent by peer p in room r.
func (m *Manager) open(r *Room, p peer.ID, data []byte) ([]byte, error) {
	return crypto.Decrypt(r.key, aad(r.Topic, p), data)
}

func (m *Manager) publishPresence(r *Room) {
	p := wirePayload{Kind: "presence", Nick: m.Nick(), TS: time.Now().Unix()}
	plain, err := json.Marshal(p)
	if err != nil {
		return
	}
	env, err := m.seal(r, plain)
	if err != nil {
		return
	}
	_ = r.topic.Publish(m.ctx, env)
}

// presenceLoop re-announces our nickname so every member hears it even when
// the first announcement races ahead of GossipSub mesh formation (common in
// the bridged WAN topology, where members are not in each other's direct mesh).
func (m *Manager) presenceLoop(r *Room, ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.publishPresence(r)
		}
	}
}

func (m *Manager) emit(ev Event) {
	select {
	case m.events <- ev:
	default:
		// UI is backlogged; drop rather than block the network goroutine.
	}
}

func (m *Manager) readMessages(r *Room, ctx context.Context) {
	for {
		msg, err := r.sub.Next(ctx)
		if err != nil {
			return
		}
		if msg.GetFrom() == m.host.ID() {
			continue // our own echo
		}
		plain, err := m.open(r, msg.GetFrom(), msg.GetData())
		if err != nil {
			continue // wrong key / not a member
		}
		var p wirePayload
		if err := json.Unmarshal(plain, &p); err != nil {
			continue
		}
		switch p.Kind {
		case "msg":
			if !m.saw(p.ID) {
				continue
			}
			ms := &Message{ID: p.ID, Room: r.Name, Nick: p.Nick, Body: p.Body, TS: p.TS}
			m.appendHistory(r.Name, ms)
			r.setNick(msg.GetFrom(), p.Nick)
			m.emit(Event{Type: EventMessage, Room: r.Name, From: msg.GetFrom(), Nick: p.Nick, Msg: ms})
		case "presence":
			m.onPresence(r, msg.GetFrom(), p.Nick)
		}
	}
}

// onPresence records a peer's announced nickname. Membership is announced via
// presence (not mesh hooks) so it works across the rendezvous bridge, where
// clients never sit in each other's GossipSub mesh.
func (m *Manager) onPresence(r *Room, pid peer.ID, nick string) {
	if nick == "" {
		nick = shortID(pid)
	}
	r.mu.Lock()
	old, known := r.members[pid]
	r.members[pid] = nick
	r.mu.Unlock()
	switch {
	case !known:
		m.emit(Event{Type: EventJoin, Room: r.Name, From: pid, Nick: nick})
	case old != nick:
		m.emit(Event{Type: EventPresence, Room: r.Name, From: pid, Nick: nick})
	}
}

func (m *Manager) readPeerEvents(r *Room, ctx context.Context) {
	for {
		evt, err := r.evh.NextPeerEvent(ctx)
		if err != nil {
			return
		}
		switch evt.Type {
		case pubsub.PeerJoin:
			// A GossipSub mesh peer appeared (a direct connection, or — in the
			// WAN topology — the rendezvous bridge). Members themselves are
			// discovered via encrypted presence messages, which we re-announce
			// here so the newcomer can learn our nickname even if our first
			// announcement raced ahead of mesh formation.
			m.publishPresence(r)
		case pubsub.PeerLeave:
			r.removeMember(evt.Peer)
			m.emit(Event{Type: EventLeave, Room: r.Name, From: evt.Peer})
		}
	}
}

// saw records a message ID and reports whether it is new.
func (m *Manager) saw(id string) bool {
	if id == "" {
		return true // unpadded messages (ancient wire format) are always accepted
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.seen[id]; dup {
		return false
	}
	m.seen[id] = struct{}{}
	m.seenOrder = append(m.seenOrder, id)
	if len(m.seenOrder) > maxSeenMessages {
		delete(m.seen, m.seenOrder[0])
		m.seenOrder = m.seenOrder[1:]
	}
	return true
}

func (m *Manager) appendHistory(name string, ms *Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h := m.history[name]
	h = append(h, *ms)
	if len(h) > maxHistoryPerRoom {
		h = h[len(h)-maxHistoryPerRoom:]
	}
	m.history[name] = h
}

func trimRoomName(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 {
		last := s[len(s)-1]
		if last == ' ' || last == '\t' {
			s = s[:len(s)-1]
			continue
		}
		break
	}
	return s
}

func shortID(p peer.ID) string {
	s := p.String()
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
