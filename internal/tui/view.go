package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"gochat/internal/room"
)

func (m *Model) mainW() int {
	w := m.width - sidebarWidth
	if w < 30 {
		w = 30
	}
	return w
}

// View implements tea.Model.
func (m *Model) View() string {
	if !m.ready {
		return "gochat is starting…"
	}

	side := m.renderSidebar()
	right := lipgloss.JoinVertical(lipgloss.Left,
		m.renderHeader(),
		styleViewport.Render(m.viewport.View()),
		m.inputBox(),
	)
	body := lipgloss.JoinHorizontal(lipgloss.Top, side, right)
	return lipgloss.JoinVertical(lipgloss.Left, body, m.renderStatus())
}

// shortPeer shortens a peer ID for display (status bar, peer lists).
func shortPeer(id string) string {
	if len(id) > 10 {
		return id[:10]
	}
	return id
}

func (m *Model) renderSidebar() string {
	var b strings.Builder
	b.WriteString(styleSidebarHdr.Render(" ROOMS"))
	b.WriteByte('\n')
	if len(m.rooms) == 0 {
		b.WriteString(styleSidebarItem.Render("  (none yet)"))
		b.WriteByte('\n')
	} else {
		for i, name := range m.rooms {
			cnt := m.mgr.MemberCount(name)
			if i == m.sel && !m.inputFocused {
				b.WriteString(styleSidebarSel.Render(fmt.Sprintf(" %s [%d]", name, cnt)))
			} else {
				b.WriteString(styleSidebarItem.Render(fmt.Sprintf(" %s [%d]", name, cnt)))
			}
			b.WriteByte('\n')
		}
	}
	b.WriteString(styleSidebarHint.Render("  ↑/↓ nav · Tab switch"))
	return styleSidebar.Render(strings.TrimSuffix(b.String(), "\n"))
}

func (m *Model) renderHeader() string {
	main := m.mainW()
	title, detail := " no room selected", "enter a room with /join"
	if m.active != "" {
		title = " " + m.active
		detail = fmt.Sprintf("%d peers", m.mgr.MemberCount(m.active))
	}
	head := lipgloss.NewStyle().
		Width(main).
		Render(styleHeader.Render(title) + "  " + styleHeaderDim.Render(detail))
	return head
}

func (m *Model) inputBox() string {
	box := styleInput
	if m.inputFocused {
		box = styleInputFocus
	}
	return box.Width(m.mainW() - 2).Render(m.input.View())
}

func (m *Model) renderStatus() string {
	peerID := shortPeer(m.node.ID().String())
	peers := m.node.PeerCount()
	left := fmt.Sprintf(" %s ▸ %s ▸ %d peers ▸ %d rooms", m.mgr.Nick(), peerID, peers, len(m.rooms))
	right := m.status
	if right == "" {
		right = "/help"
	}
	main := m.width
	pad := main - lipgloss.Width(left) - lipgloss.Width(right) - 2
	if pad < 1 {
		pad = 1
	}
	return styleStatus.Render(left + strings.Repeat(" ", pad) + right)
}

func (m *Model) renderMessages() string {
	if m.active == "" {
		return ""
	}
	msgs := m.mgr.Messages(m.active)
	if len(msgs) == 0 {
		return styleSys.Render("no messages yet — the room is quiet. Say hi!")
	}
	var b strings.Builder
	for i := range msgs {
		b.WriteString(m.renderMsg(&msgs[i]))
		b.WriteByte('\n')
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func (m *Model) renderMsg(ms *room.Message) string {
	t := "        "
	if ms.TS > 0 {
		t = time.Unix(ms.TS, 0).Format("15:04:05")
	}
	nick := ms.Nick
	if nick == "" {
		nick = "(anon)"
	}
	nickPart := styleNick(ms.Nick).
		Render(nick + ":")
	body := styleBody.Render(ms.Body)
	return lipgloss.JoinHorizontal(lipgloss.Top,
		styleTime.Render("["+t+"] "),
		nickPart,
		" ",
		body,
	)
}
