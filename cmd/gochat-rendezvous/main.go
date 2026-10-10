// Command gochat-rendezvous is the server-side rendezvous/relay node that lets
// gochat users meet over the internet. It never sees room names or message
// contents: rooms are referenced by hashed topics and messages are encrypted.
//
// Run it on a machine with a public IP, then tell clients about it:
//
//	gochat-rendezvous -port 4001 -public 203.0.113.5
//	gochat -bootstrap "/ip4/203.0.113.5/tcp/4001/p2p/<printed peer id>"
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/multiformats/go-multiaddr"

	"github.com/Satyam-1o/gochat/internal/identity"
	"github.com/Satyam-1o/gochat/internal/node"
	"github.com/Satyam-1o/gochat/internal/version"
)

type stringSlice []string

func (s *stringSlice) String() string     { return fmt.Sprint([]string(*s)) }
func (s *stringSlice) Set(v string) error { *s = append(*s, v); return nil }

func main() {
	var announce stringSlice
	port := flag.Int("port", 4001, "listen port")
	ident := flag.String("identity", "rendezvous.key", "identity key file (created on first run)")
	dataDir := flag.String("data", "", "directory holding the identity key (default: current dir)")
	public := flag.String("public", "", "public host/IP to print in client-ready addresses (optional)")
	var extra stringSlice
	flag.Var(&extra, "listen", "extra listen multiaddr (repeatable, e.g. /ip4/0.0.0.0/udp/4001/quic-v1)")
	flag.Var(&announce, "announce", "explicit announce multiaddr with peer ID (repeatable, e.g. /dns4/chat.example.com/tcp/4001/p2p/<id>)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("gochat-rendezvous %s\n", version.String())
		return
	}

	dir := *dataDir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Fatalf("data dir: %v", err)
	}
	keyPath := filepath.Join(dir, *ident)

	priv, err := identity.LoadOrCreate(keyPath)
	if err != nil {
		log.Fatalf("identity: %v", err)
	}

	listen := []string{
		fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", *port),
		fmt.Sprintf("/ip4/0.0.0.0/udp/%d/quic-v1", *port),
	}
	listen = append(listen, extra...)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nd, err := node.New(ctx, node.Options{
		PrivKey:     priv,
		ListenAddrs: listen,
		RelayServer: true,
		MDNS:        false,
		UserAgent:   "gochat-rendezvous",
	})
	if err != nil {
		log.Fatalf("node: %v", err)
	}
	defer nd.Close()

	peerID := nd.ID()
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  gochat-rendezvous "+version.Version+" — PEX + circuit relay node")
	fmt.Fprintf(os.Stderr, "  peer ID : %s\n", peerID)

	if len(announce) > 0 {
		fmt.Fprintln(os.Stderr, "  announce addresses:")
		for _, a := range announce {
			if _, err := multiaddr.NewMultiaddr(a); err != nil {
				log.Printf("warning: ignoring bad -announce %q: %v", a, err)
				continue
			}
			fmt.Fprintf(os.Stderr, "    %s\n", a)
			fmt.Fprintf(os.Stderr, "    client: gochat -bootstrap %q\n", a)
		}
	} else {
		fmt.Fprintln(os.Stderr, "  listening addresses:")
		for _, a := range nd.Addrs() {
			fmt.Fprintf(os.Stderr, "    %s/p2p/%s\n", a, peerID)
		}
		if *public != "" {
			fmt.Fprintln(os.Stderr, "  client-ready address (public host):")
			for _, a := range nd.Addrs() {
				patched := replaceHost(a, *public)
				full := patched + "/p2p/" + peerID.String()
				fmt.Fprintf(os.Stderr, "    %s\n", full)
				fmt.Fprintf(os.Stderr, "    client: gochat -bootstrap %q\n", full)
			}
		}
	}
	fmt.Fprintln(os.Stderr, "  relay service + PEX server + pubsub bridge running (Ctrl+C to stop)")

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	fmt.Fprintln(os.Stderr, "shutting down…")
}

// replaceHost swaps the first host component (ip4/ip6/dns*) of a multiaddr
// for the given public host, preserving everything else (including /p2p/...).
func replaceHost(addr string, host string) string {
	maddr, err := multiaddr.NewMultiaddr(addr)
	if err != nil {
		log.Printf("warning: cannot parse %q: %v", addr, err)
		return addr
	}
	hostProto := "ip4"
	if indexOfColon(host) >= 0 {
		hostProto = "ip6"
	}
	var b strings.Builder
	replaced := false
	multiaddr.ForEach(maddr, func(c multiaddr.Component) bool {
		code := c.Protocol().Code
		isHostPart := code == multiaddr.P_IP4 || code == multiaddr.P_IP6 ||
			code == multiaddr.P_DNS || code == multiaddr.P_DNS4 || code == multiaddr.P_DNS6
		if isHostPart && !replaced {
			replaced = true
			b.WriteString("/" + hostProto + "/" + host)
		} else if c.Value() == "" {
			// Protocols without a value (e.g. quic-v1, tls) must not gain a
			// trailing slash.
			b.WriteString("/" + c.Protocol().Name)
		} else {
			b.WriteString("/" + c.Protocol().Name + "/" + c.Value())
		}
		return true
	})
	return b.String()
}

func indexOfColon(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return i
		}
	}
	return -1
}
