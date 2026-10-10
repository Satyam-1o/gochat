package store

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestConfigRoundtrip(t *testing.T) {
	s := newTestStore(t)

	cfg := s.LoadConfig()
	if cfg.DisplayName != "" || len(cfg.Bootstrap) != 0 {
		t.Fatalf("expected empty config, got %+v", cfg)
	}

	cfg.DisplayName = "alice"
	cfg.Bootstrap = []string{"/ip4/1.2.3.4/tcp/4001/p2p/QmFoo"}
	if err := s.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	got := s.LoadConfig()
	if got.DisplayName != "alice" || len(got.Bootstrap) != 1 {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
}

func TestRoomsRoundtripAndUpsert(t *testing.T) {
	s := newTestStore(t)

	key := strings.Repeat("A", 44) // a fake base64 32-byte key
	r1 := Room{Name: "lobby", Topic: "/gochat/1.0/AAAA", Key: key, CreatedAt: time.Now()}
	r2 := Room{Name: "dev", Topic: "/gochat/1.0/BBBB", Key: key, CreatedAt: time.Now()}

	added, err := s.UpsertRoom(r1)
	if err != nil || !added {
		t.Fatalf("first upsert: added=%v err=%v", added, err)
	}
	if _, err := s.UpsertRoom(r2); err != nil {
		t.Fatal(err)
	}

	rooms, err := s.LoadRooms()
	if err != nil {
		t.Fatal(err)
	}
	if len(rooms) != 2 {
		t.Fatalf("want 2 rooms, got %d", len(rooms))
	}

	// Upsert same name replaces.
	r1b := r1
	r1b.Topic = "/gochat/1.0/UPDATED"
	added, err = s.UpsertRoom(r1b)
	if err != nil || added {
		t.Fatalf("second upsert should replace (added=%v err=%v)", added, err)
	}
	rooms, _ = s.LoadRooms()
	if len(rooms) != 2 {
		t.Fatalf("want 2 rooms after replace, got %d", len(rooms))
	}
	found := false
	for _, r := range rooms {
		if r.Name == "lobby" && r.Topic == "/gochat/1.0/UPDATED" {
			found = true
		}
	}
	if !found {
		t.Fatal("upsert did not replace topic")
	}

	if err := s.DeleteRoom("lobby"); err != nil {
		t.Fatal(err)
	}
	rooms, _ = s.LoadRooms()
	if len(rooms) != 1 || rooms[0].Name != "dev" {
		t.Fatalf("delete failed: %+v", rooms)
	}
}

func TestRoomKeyBytes(t *testing.T) {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i)
	}
	kb, err := Room{Key: base64.StdEncoding.EncodeToString(raw)}.KeyBytes()
	if err != nil {
		t.Fatal(err)
	}
	if len(kb) != 32 {
		t.Fatalf("decoded key len = %d, want 32", len(kb))
	}
	if !bytes.Equal(kb, raw) {
		t.Fatal("key roundtrip mismatch")
	}
}

func TestFilePermissions(t *testing.T) {
	s := newTestStore(t)

	if err := s.SaveConfig(Config{DisplayName: "x"}); err != nil {
		t.Fatal(err)
	}
	roomsPath := filepath.Join(s.Dir(), "rooms.json")
	if _, err := s.UpsertRoom(Room{Name: "a", Topic: "t", Key: "k"}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(s.Dir(), "config.json"), roomsPath} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0o077 != 0 {
			t.Fatalf("%s has group/other read bits: %v", p, fi.Mode().Perm())
		}
	}
}
