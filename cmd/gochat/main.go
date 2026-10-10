// Command gochat is the p2p terminal chat client.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"gochat/internal/identity"
	"gochat/internal/node"
	"gochat/internal/room"
	"gochat/internal/store"
	"gochat/internal/tui"
)

const version = "0.1.0"

func main() {
	fs := flag.NewFlagSet("gochat", flag.ExitOnError)
	configDir := fs.String("config", "", "data directory (default: OS config dir/gochat)")
	nick := fs.String("nick", "", "display name (overrides saved config)")
	bootstrap := fs.String("bootstrap", "", "comma-separated rendezvous/relay node multiaddrs (overrides saved config)")
	showVersion := fs.Bool("version", false, "print version and exit")

	// Headless mode: join a room without the TUI, useful for scripts and CI.
	headless := fs.Bool("headless", false, "run without the TUI (for scripts/CI)")
	roomName := fs.String("room", "", "room to join (headless mode)")
	pass := fs.String("pass", "", "room passphrase (headless mode; visible in process list — prefer -pass-file)")
	passFile := fs.String("pass-file", "", "file containing the room passphrase (headless mode)")
	sendMsg := fs.String("send", "", "message to send periodically (headless mode)")
	expect := fs.String("expect", "", "substring to wait for before exiting (headless mode)")
	timeout := fs.Duration("timeout", 30*time.Second, "headless run timeout")
	_ = fs.Parse(os.Args[1:])

	if *showVersion {
		fmt.Printf("gochat %s\n", version)
		return
	}

	dir := *configDir
	if dir == "" {
		d, err := store.DefaultDir()
		if err != nil {
			fatal(err)
		}
		dir = d
	}

	passphrase, err := resolvePassphrase(*pass, *passFile)
	if err != nil {
		fatal(err)
	}

	if *headless {
		if err := runHeadless(dir, *nick, *bootstrap, *roomName, passphrase, *sendMsg, *expect, *timeout); err != nil {
			fatal(err)
		}
		return
	}

	if err := run(dir, *nick, *bootstrap); err != nil {
		fatal(err)
	}
}

// resolvePassphrase returns the passphrase from an explicit value or a file.
func resolvePassphrase(pass, passFile string) (string, error) {
	if passFile != "" {
		b, err := os.ReadFile(passFile)
		if err != nil {
			return "", fmt.Errorf("read passphrase file: %w", err)
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	}
	return pass, nil
}

func run(dir, nick, bootstrapFlag string) error {
	st, err := store.Open(dir)
	if err != nil {
		return err
	}

	cfg := st.LoadConfig()
	if nick != "" {
		cfg.DisplayName = nick
	}
	if bootstrapFlag != "" {
		cfg.Bootstrap = splitCSV(bootstrapFlag)
	}
	if err := st.SaveConfig(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	priv, err := identity.LoadOrCreate(filepath.Join(dir, "identity"))
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nd, err := node.New(ctx, node.Options{
		PrivKey:     priv,
		Bootstrap:   cfg.Bootstrap,
		MDNS:        true,
		MDNSService: "gochat",
		UserAgent:   "gochat/" + version,
	})
	if err != nil {
		return err
	}
	defer nd.Close()

	mgr, err := room.NewManager(ctx, nd.Host, nd, st)
	if err != nil {
		return err
	}
	defer mgr.Close()

	saved := 0
	if rooms, err := st.LoadRooms(); err == nil {
		saved = len(rooms)
	}
	banner(nd, cfg, saved)

	if err := mgr.Restore(); err != nil {
		log.Printf("warning: restore failed: %v", err)
	}

	p := tea.NewProgram(tui.NewModel(mgr, nd), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	return nil
}

func banner(nd *node.Node, cfg store.Config, restored int) {
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "  gochat "+version+" — p2p terminal chat")
	fmt.Fprintf(os.Stderr, "  peer ID   : %s\n", nd.ID())
	fmt.Fprintf(os.Stderr, "  listening : %s / etc\n", shortAddr(nd.Addrs()))
	fmt.Fprintf(os.Stderr, "  rooms saved: %d (auto-joining…)\n", restored)
	if len(cfg.Bootstrap) == 0 {
		fmt.Fprintln(os.Stderr, "  rendezvous: none configured — internet discovery disabled")
		fmt.Fprintln(os.Stderr, "               add one with: gochat -bootstrap /ip4/<public>/tcp/<port>/p2p/<peerID>")
	} else {
		fmt.Fprintf(os.Stderr, "  rendezvous: %d bootstrap node(s)\n", len(cfg.Bootstrap))
	}
	fmt.Fprintln(os.Stderr)
}

func shortAddr(addrs []string) string {
	if len(addrs) == 0 {
		return "none"
	}
	return addrs[0]
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "gochat: %v\n", err)
	os.Exit(1)
}

// runHeadless joins a room without the TUI. It repeatedly sends -send (if set)
// and, when -expect is set, exits successfully as soon as a chat message
// containing that substring arrives. This powers scripts/smoke-test.sh and
// lets users verify their rendezvous node end to end.
func runHeadless(dir, nick, bootstrapFlag, roomName, passphrase, send, expect string, timeout time.Duration) error {
	if roomName == "" || passphrase == "" {
		return fmt.Errorf("headless: -room and -pass (or -pass-file) are required")
	}

	st, err := store.Open(dir)
	if err != nil {
		return err
	}
	cfg := st.LoadConfig()
	if nick != "" {
		cfg.DisplayName = nick
	}
	if bootstrapFlag != "" {
		cfg.Bootstrap = splitCSV(bootstrapFlag)
	}
	if err := st.SaveConfig(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	priv, err := identity.LoadOrCreate(filepath.Join(dir, "identity"))
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout+15*time.Second)
	defer cancel()

	nd, err := node.New(ctx, node.Options{
		PrivKey:   priv,
		Bootstrap: cfg.Bootstrap,
		MDNS:      false,
		UserAgent: "gochat-headless/" + version,
	})
	if err != nil {
		return err
	}
	defer nd.Close()

	mgr, err := room.NewManager(ctx, nd.Host, nd, st)
	if err != nil {
		return err
	}
	defer mgr.Close()

	if nick != "" {
		mgr.SetNick(nick)
	}
	if err := mgr.Join(roomName, passphrase); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "gochat-headless: joined %q as %q (peer %s)\n", roomName, mgr.Nick(), nd.ID())

	if send != "" {
		go func() {
			// Wait a moment for the mesh/bridge to settle, then keep sending
			// so a late-joining peer still receives the message.
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			for {
				_ = mgr.Send(roomName, send)
				select {
				case <-ctx.Done():
					return
				case <-time.After(2 * time.Second):
				}
			}
		}()
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("headless: timed out waiting for %q", expect)
		case <-deadline.C:
			if expect == "" {
				return nil
			}
			return fmt.Errorf("headless: timed out waiting for %q", expect)
		case ev := <-mgr.Events():
			switch ev.Type {
			case room.EventMessage:
				if ev.Msg == nil {
					continue
				}
				if ev.Msg.Own {
					// Our own local echo; not something we received.
					fmt.Printf("SENT %s\n", ev.Msg.Body)
					continue
				}
				fmt.Printf("RECV %s: %s\n", ev.Msg.Nick, ev.Msg.Body)
				if expect != "" && strings.Contains(ev.Msg.Body, expect) {
					fmt.Printf("OK: received %q\n", expect)
					return nil
				}
			case room.EventJoin:
				fmt.Fprintf(os.Stderr, "PEER JOIN: %s\n", ev.Nick)
			}
		}
	}
}
