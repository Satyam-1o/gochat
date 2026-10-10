package node

import (
	"context"
	"log"
	"sync"

	"github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
)

// pubsubBridge subscribes the rendezvous node to every topic advertised by
// PEX clients. Because every client is connected to this node, messages
// reliably flow client -> node -> other clients even when two clients cannot
// dial each other directly (e.g. both behind NAT). The node only ever sees
// the hashed topic + ciphertext, never room names or plaintext.
//
// Relay caveat: GossipSub only forwards messages on topics it is actively
// subscribed to. A bare Join() is not enough, so the bridge keeps a live
// subscription per topic and drains it into a sink thread.
type pubsubBridge struct {
	ctx    context.Context
	ps     *pubsub.PubSub
	mu     sync.Mutex
	topics map[string]*pubsub.Topic
}

func newPubsubBridge(ctx context.Context, h host.Host) (*pubsubBridge, error) {
	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		return nil, err
	}
	return &pubsubBridge{ctx: ctx, ps: ps, topics: make(map[string]*pubsub.Topic)}, nil
}

// join subscribes to a namespace's pubsub topic. Idempotent.
func (b *pubsubBridge) join(ns string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.topics[ns]; ok {
		return
	}
	t, err := b.ps.Join(ns)
	if err != nil {
		log.Printf("bridge: join topic %q: %v", ns, err)
		return
	}
	sub, err := t.Subscribe()
	if err != nil {
		log.Printf("bridge: subscribe topic %q: %v", ns, err)
		_ = t.Close()
		return
	}
	b.topics[ns] = t
	go b.drain(ns, sub)
}

// drain consumes and discards bridged messages; the subscription must stay
// live for the node to relay the topic.
func (b *pubsubBridge) drain(ns string, sub *pubsub.Subscription) {
	for {
		if _, err := sub.Next(b.ctx); err != nil {
			log.Printf("bridge: drain topic %q: %v", ns, err)
			return
		}
	}
}

// leave unsubscribes from a namespace's pubsub topic. Idempotent.
func (b *pubsubBridge) leave(ns string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if t, ok := b.topics[ns]; ok {
		err := t.Close() // also cancels the drain subscription
		if err != nil {
			log.Printf("bridge: leave topic %q: %v", ns, err)
		}
		delete(b.topics, ns)
	}
}
