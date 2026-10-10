// Package store persists the small amount of state gochat keeps on disk:
// the room list (so rooms survive restarts) and app config. Messages are
// ephemeral and never touch the disk.
package store

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Config is the app-level configuration.
type Config struct {
	// DisplayName is the nickname users see, should it be empty the UI
	// falls back to a short form of the peer ID.
	DisplayName string `json:"display_name"`
	// Bootstrap holds multiaddrs (with /p2p/<peerID>) of rendezvous/relay
	// nodes used for internet-wide discovery.
	Bootstrap []string `json:"bootstrap"`
}

// Room is a saved room: enough to rejoin automatically without the passphrase.
type Room struct {
	Name      string    `json:"name"`
	Topic     string    `json:"topic"` // precomputed, never re-derived
	Key       string    `json:"key"`   // base64 of the derived room key
	CreatedAt time.Time `json:"created_at"`
}

// KeyBytes returns the decoded room key.
func (r Room) KeyBytes() ([]byte, error) {
	return base64.StdEncoding.DecodeString(r.Key)
}

// Store reads and writes config + room state under one directory using
// atomic writes (temp file + rename) and restrictive permissions.
type Store struct {
	dir string
	mu  sync.Mutex
}

// DefaultDir returns the standard config directory for gochat.
func DefaultDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.Getenv("HOME")
		if base == "" {
			return "", errors.New("store: cannot determine a config directory")
		}
	}
	return filepath.Join(base, "gochat"), nil
}

// Open ensures dir exists and returns a Store for it.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("store: create dir: %w", err)
	}
	return &Store{dir: dir}, nil
}

func (s *Store) configPath() string { return filepath.Join(s.dir, "config.json") }
func (s *Store) roomsPath() string  { return filepath.Join(s.dir, "rooms.json") }

// Dir returns the underlying storage directory.
func (s *Store) Dir() string { return s.dir }

// LoadConfig returns the current config (zero value if none exists).
func (s *Store) LoadConfig() Config {
	s.mu.Lock()
	defer s.mu.Unlock()

	var cfg Config
	data, err := os.ReadFile(s.configPath())
	if err != nil {
		return cfg
	}
	_ = json.Unmarshal(data, &cfg)
	return cfg
}

// SaveConfig atomically persists cfg.
func (s *Store) SaveConfig(cfg Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return writeJSON(s.configPath(), cfg)
}

// LoadRooms returns all saved rooms.
func (s *Store) LoadRooms() ([]Room, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.roomsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var rooms []Room
	if err := json.Unmarshal(data, &rooms); err != nil {
		return nil, fmt.Errorf("store: parse rooms: %w", err)
	}
	sort.Slice(rooms, func(i, j int) bool { return rooms[i].Name < rooms[j].Name })
	return rooms, nil
}

// UpsertRoom adds a room or updates an existing one with the same name.
// Returns true when the room was newly added.
func (s *Store) UpsertRoom(room Room) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rooms, err := loadRoomsLocked(s.roomsPath())
	if err != nil {
		return false, err
	}
	added := true
	for i := range rooms {
		if rooms[i].Name == room.Name {
			rooms[i] = room
			added = false
			break
		}
	}
	if added {
		rooms = append(rooms, room)
	}
	sort.Slice(rooms, func(i, j int) bool { return rooms[i].Name < rooms[j].Name })
	return added, writeJSON(s.roomsPath(), rooms)
}

// DeleteRoom removes a saved room by name.
func (s *Store) DeleteRoom(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rooms, err := loadRoomsLocked(s.roomsPath())
	if err != nil {
		return err
	}
	out := rooms[:0]
	for _, r := range rooms {
		if r.Name != name {
			out = append(out, r)
		}
	}
	return writeJSON(s.roomsPath(), out)
}

func loadRoomsLocked(path string) ([]Room, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var rooms []Room
	if err := json.Unmarshal(data, &rooms); err != nil {
		return nil, fmt.Errorf("store: parse rooms: %w", err)
	}
	return rooms, nil
}

// writeJSON writes v to path atomically (temp file + rename) with 0600 perms.
func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("store: encode: %w", err)
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
