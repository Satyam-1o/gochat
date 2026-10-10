package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// runCommand handles slash-commands from the input line.
// It returns true when the app should quit.
func (m *Model) runCommand(raw string) bool {
	cmd, rest := splitCommand(raw)
	switch cmd {
	case "help":
		m.showHelp()
	case "join":
		m.cmdJoin(rest)
	case "leave":
		m.cmdLeave()
	case "nick":
		m.cmdNick(rest)
	case "rooms":
		m.showRooms()
	case "peers":
		m.showPeers()
	case "clear":
		m.viewport.SetContent("")
	case "quit", "exit":
		return true
	default:
		m.status = fmt.Sprintf("unknown command /%s — try /help", cmd)
	}
	return false
}

func (m *Model) cmdJoin(rest string) {
	rest = strings.TrimSpace(rest)
	sp := strings.IndexByte(rest, ' ')
	if sp <= 0 {
		m.status = "usage: /join <room> <passphrase>"
		return
	}
	name := strings.TrimSpace(rest[:sp])
	pass := strings.TrimSpace(rest[sp+1:])
	if name == "" || pass == "" {
		m.status = "usage: /join <room> <passphrase>"
		return
	}
	if err := m.mgr.Join(name, pass); err != nil {
		m.status = "join: " + err.Error()
		return
	}
	m.refreshRooms()
	if m.active == "" {
		m.setActive(name)
	}
	m.status = fmt.Sprintf("joined %q", name)
}

func (m *Model) cmdLeave() {
	if m.active == "" {
		m.status = "nothing selected to leave"
		return
	}
	name := m.active
	if err := m.mgr.Leave(name); err != nil {
		m.status = "leave: " + err.Error()
		return
	}
	m.status = fmt.Sprintf("left %q", name)
}

func (m *Model) cmdNick(rest string) {
	nick := strings.TrimSpace(rest)
	if nick == "" {
		m.status = "usage: /nick <name>"
		return
	}
	m.mgr.SetNick(nick)
	m.status = fmt.Sprintf("nickname is now %q", nick)
}

func (m *Model) showHelp() {
	m.viewport.SetContent(lipgloss.NewStyle().Padding(0, 2).Render(helpText))
	m.viewport.GotoTop()
}

func (m *Model) showRooms() {
	var b strings.Builder
	names := m.mgr.RoomNames()
	if len(names) == 0 {
		b.WriteString("(no rooms yet)\n")
	} else {
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintf(&b, "• %s  (%d peers)\n", n, m.mgr.MemberCount(n))
		}
	}
	m.viewport.SetContent(lipgloss.NewStyle().Padding(0, 2).Render(strings.TrimSuffix(b.String(), "\n")))
	m.viewport.GotoTop()
}

func (m *Model) showPeers() {
	if m.active == "" {
		m.viewport.SetContent("no active room")
		m.viewport.GotoTop()
		return
	}
	members := m.mgr.MembersMap(m.active)
	if len(members) == 0 {
		m.viewport.SetContent("(no peers connected yet)")
		m.viewport.GotoTop()
		return
	}
	var b strings.Builder
	ids := make([]string, 0, len(members))
	for id := range members {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		nick := members[id]
		if nick == "" {
			nick = "(unknown)"
		}
		fmt.Fprintf(&b, "%s  %s\n", nick, shortPeer(id))
	}
	m.viewport.SetContent(lipgloss.NewStyle().Padding(0, 2).Render(strings.TrimSuffix(b.String(), "\n")))
	m.viewport.GotoTop()
}

// splitCommand splits "/join rest of line" into ("join", "rest of line").
func splitCommand(raw string) (string, string) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "/")
	sp := strings.IndexByte(raw, ' ')
	if sp < 0 {
		return raw, ""
	}
	return raw[:sp], strings.TrimSpace(raw[sp+1:])
}

const helpText = `gochat — p2p terminal chat

ROOMS
  /join <room> <passphrase>  join (or create) a private room
  /leave                     leave the active room
  /rooms                     list saved rooms
  /peers                     list peers in the active room

YOU
  /nick <name>               change your display name

MISC
  /clear                     clear the message pane
  /help                      show this help
  /quit                      exit

KEYS
  Enter    send message (Shift+Enter is not needed: one-line messages)
  Tab/Esc  switch focus between sidebar and input
  ↑ / ↓    select room in sidebar (when focused)
  PgUp/Dn  scroll the message pane
  Ctrl+C   quit

Rooms are saved and auto-rejoined on the next launch; messages are
ephemeral and disappear when you quit.

Reminder: everyone who wants to talk must use the SAME room name and passphrase.
(Passphrase-based rooms are not visible on the network; the topic is a hash.)

Privacy note: messages are encrypted end-to-end within the room (XChaCha20-Poly1305),
but the rendezvous node sees peer addresses and traffic metadata.
`
