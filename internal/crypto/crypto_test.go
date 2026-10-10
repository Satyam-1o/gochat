package crypto

import (
	"bytes"
	"testing"
)

func TestDeriveRoomKeyDeterministic(t *testing.T) {
	a := DeriveRoomKey("hunter2")
	b := DeriveRoomKey("hunter2")
	if !bytes.Equal(a, b) {
		t.Fatal("same passphrase produced different keys")
	}
	if bytes.Equal(a, DeriveRoomKey("hunter3")) {
		t.Fatal("different passphrases produced the same key")
	}
	if len(a) != KeyLen {
		t.Fatalf("key length = %d, want %d", len(a), KeyLen)
	}
}

func TestRoomTopicStableAndSecret(t *testing.T) {
	key := DeriveRoomKey("correct horse battery staple")
	t1 := RoomTopic("lobby", key)
	t2 := RoomTopic("lobby", key)
	if t1 != t2 {
		t.Fatalf("topic not stable: %q vs %q", t1, t2)
	}
	otherTopic := RoomTopic("lobby", DeriveRoomKey("wrong passphrase"))
	if t1 == otherTopic {
		t.Fatal("topic leaked passphrase difference")
	}
	otherNameTopic := RoomTopic("other-room", key)
	if t1 == otherNameTopic {
		t.Fatal("topic leaked room name difference")
	}
	if bytes.Contains([]byte(t1), []byte("lobby")) {
		t.Fatal("topic contains the room name")
	}
}

func TestMessageKeyDiffersPerRoom(t *testing.T) {
	key := DeriveRoomKey("pw")
	k1 := MessageKey(key, "a")
	k2 := MessageKey(key, "b")
	if bytes.Equal(k1, k2) {
		t.Fatal("message keys must differ per room")
	}
}

func TestEncryptDecryptRoundtrip(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	aad := []byte("peerA/gochat/1.0/abc")
	payload := []byte(`{"kind":"msg","body":"hello world"}`)

	sealed, err := Encrypt(key, aad, payload)
	if err != nil {
		t.Fatal(err)
	}
	open, err := Decrypt(key, aad, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(open, payload) {
		t.Fatal("roundtrip mismatch")
	}
}

func TestEncryptRejectsWrongKeyOrAAD(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	other := []byte("00000000000000000000000000000000")
	aad := []byte("peerA/topic")

	sealed, err := Encrypt(key, aad, []byte("top secret"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Decrypt(other, aad, sealed); err == nil {
		t.Fatal("decrypt with wrong key succeeded")
	}
	if _, err := Decrypt(key, []byte("peerB/topic"), sealed); err == nil {
		t.Fatal("decrypt with wrong AAD succeeded")
	}
	if _, err := Decrypt(key, aad, sealed[:5]); err == nil {
		t.Fatal("decrypt of truncated data succeeded")
	}
}

func TestNonceRandomness(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	a, _ := Encrypt(key, nil, []byte("x"))
	b, _ := Encrypt(key, nil, []byte("x"))
	if bytes.Equal(a, b) {
		t.Fatal("two encryptions of the same data produced identical output (nonce reuse)")
	}
}
