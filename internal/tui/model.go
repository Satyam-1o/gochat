package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Satyam-1o/gochat/internal/node"
	"github.com/Satyam-1o/gochat/internal/room"
)

// Layout constants.
const (
	sidebarWidth = 22
	headerHeight = 1
	statusHeight = 1
)

// Model is the root Bubble Tea model.
type Model struct {
	mgr  *room.Manager
	node *node.Node

	width  int
	height int
	ready  bool

	viewport viewport.Model
	input    textinput.Model
	status   string

	rooms        []string
	sel          int
	active       string
	inputFocused bool

	events <-chan room.Event
}

// NewModel constructs the TUI.
func NewModel(mgr *room.Manager, nd *node.Node) *Model {
	ti := textinput.New()
	ti.Placeholder = "Type a message…  (/help for commands)"
	ti.CharLimit = 4000
	ti.Prompt = "» "

	m := &Model{
		mgr:          mgr,
		node:         nd,
		input:        ti,
		events:       mgr.Events(),
		inputFocused: true,
	}
	m.refreshRooms()
	if len(m.rooms) > 0 {
		m.setActive(m.rooms[0])
	}
	return m
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd {
	m.input.Focus()
	return tea.Batch(m.waitEvent(), textinput.Blink)
}

func (m *Model) waitEvent() tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-m.events
		if !ok {
			return eventShutdown{}
		}
		return ev
	}
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)

	case eventShutdown:
		return m, tea.Quit

	case room.Event:
		m.handleRoomEvent(msg)
		cmds = append(cmds, m.waitEvent())

	case errorMsg:
		m.status = msg.err.Error()

	case tea.KeyMsg:
		if cmd := m.handleKey(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}

	default:
		// Non-key messages (blink ticks, etc.) are routed to the focused widget.
		if m.inputFocused {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			cmds = append(cmds, cmd)
		} else {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "ctrl+c":
		return tea.Quit
	case "ctrl+d":
		if !m.inputFocused {
			return tea.Quit
		}
	}

	if m.inputFocused {
		switch msg.String() {
		case "enter":
			return m.submitInput()
		case "tab", "esc":
			m.inputFocused = false
			m.input.Blur()
			if m.sel >= len(m.rooms) {
				m.sel = 0
			}
			return nil
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return cmd
		}
	}

	// Sidebar focused.
	switch msg.String() {
	case "up":
		if m.sel > 0 {
			m.sel--
		}
		return nil
	case "down":
		if m.sel < len(m.rooms)-1 {
			m.sel++
		}
		return nil
	case "enter":
		if m.sel >= 0 && m.sel < len(m.rooms) {
			m.setActive(m.rooms[m.sel])
		}
		return nil
	case "tab", "esc":
		m.inputFocused = true
		m.input.Focus()
		return nil
	default:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return cmd
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func (m *Model) submitInput() tea.Cmd {
	raw := m.input.Value()
	m.input.SetValue("")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if strings.HasPrefix(raw, "/") {
		if m.runCommand(raw) {
			return tea.Quit
		}
		return nil
	}
	if m.active == "" {
		m.status = "join a room first: /join <room> <passphrase>"
		return nil
	}
	if err := m.mgr.Send(m.active, raw); err != nil {
		m.status = err.Error()
	}
	return nil
}

func (m *Model) setActive(name string) {
	m.active = name
	m.viewport.SetContent(m.renderMessages())
	m.viewport.GotoBottom()
}

func (m *Model) refreshRooms() {
	m.rooms = m.mgr.RoomNames()
	if m.sel >= len(m.rooms) {
		m.sel = len(m.rooms) - 1
	}
	if m.sel < 0 {
		m.sel = 0
	}
}

func (m *Model) handleRoomEvent(ev room.Event) {
	switch ev.Type {
	case room.EventMessage:
		if ev.Room == m.active {
			atBottom := m.viewport.VisibleLineCount() == 0 ||
				m.viewport.ScrollPercent() > 0.92
			m.viewport.SetContent(m.renderMessages())
			if atBottom {
				m.viewport.GotoBottom()
			}
		}
	case room.EventRoomJoined:
		m.refreshRooms()
		if m.active == "" {
			m.setActive(ev.Room)
		}
	case room.EventRoomLeft:
		m.refreshRooms()
		if m.active == ev.Room {
			if len(m.rooms) > 0 {
				m.sel = 0
				m.setActive(m.rooms[0])
			} else {
				m.active = ""
				m.viewport.SetContent("")
			}
		}
	case room.EventError:
		m.status = ev.Err.Error()
	}
}

func (m *Model) resize(w, h int) {
	m.width = w
	m.height = h

	mainW := w - sidebarWidth
	if mainW < 30 {
		mainW = 30
	}
	vpHeight := h - headerHeight - 1 - statusHeight - 2 // -1 input line frame, -2 borders
	if vpHeight < 3 {
		vpHeight = 3
	}
	m.viewport.Width = mainW
	m.viewport.Height = vpHeight
	m.input.Width = mainW
	m.viewport.SetContent(m.renderMessages())
	m.viewport.GotoBottom()
	m.ready = true
}

// eventShutdown signals that the events channel closed.
type eventShutdown struct{}

// errorMsg carries a non-fatal error to the status bar.
type errorMsg struct{ err error }
