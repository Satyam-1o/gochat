// Package crypto provides the passphrase-based room cryptography:
//   - Argon2id key derivation (the room secret)
//   - unguessable topic names derived from the room key + name
//   - XChaCha20-Poly1305 message encryption with AAD binding
//
// The invariant is that room names and passphrases never travel on the wire:
// topics are hashes, and payloads are encrypted.
package crypto

import (
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"errors"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"
)

const (
	// KeyLen is the length of the derived room key (and message key) in bytes.
	KeyLen = 32

	// Argon2id cost parameters. Deliberately moderate: this runs only when a
	// user *joins* a room, never on reconnect (the derived key is stored).
	argonMemory  = 64 * 1024 // 64 MiB
	argonTime    = 3
	argonThreads = 4
)

// appSalt is a fixed application salt. This is fine here because the
// passphrase itself is the secret; the salt just guards against rainbow
// tables across different applications that reuse the same passphrase.
var appSalt = sha256.Sum256([]byte("gochat/v1"))

// topicPrefix separates gochat topics from any other libp2p pubsub traffic.
const topicPrefix = "/gochat/1.0/"

// DeriveRoomKey derives the 32-byte room key from a passphrase using Argon2id.
// It is deterministic: every member typing the same passphrase gets the same key.
func DeriveRoomKey(passphrase string) []byte {
	if passphrase == "" {
		passphrase = " " // guard against nil-ish input producing all-zero key material
	}
	return argon2.IDKey([]byte(passphrase), appSalt[:], argonTime, argonMemory, argonThreads, KeyLen)
}

// RoomTopic derives the pubsub topic for a room. The topic depends on the room
// key, so it is unguessable without the passphrase and hides the room name.
func RoomTopic(roomName string, key []byte) string {
	h := sha256.New()
	h.Write(key)
	h.Write([]byte(roomName))
	digest := h.Sum(nil)
	return topicPrefix + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(digest[:16])
}

// MessageKey derives the per-message symmetric key from the room key and room name.
func MessageKey(roomKey []byte, roomName string) []byte {
	// crypto/hkdf.Key (stdlib, Go 1.24+) — deterministically expands roomKey.
	const infoPrefix = "gochat-msg|"
	out, err := hkdf.Key(sha256.New, roomKey, nil, infoPrefix+roomName, KeyLen)
	if err != nil {
		panic(err) // sha256-based HKDF with a small output cannot fail
	}
	return out
}

// Encrypt seals plaintext with XChaCha20-Poly1305 using key and binds aad
// (additional authenticated data). The returned blob is nonce || ciphertext.
func Encrypt(key, aad, plaintext []byte) ([]byte, error) {
	ae, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, ae.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return ae.Seal(nonce, nonce, plaintext, aad), nil
}

// Decrypt opens ciphertext (nonce || ct) using key, authenticating aad.
func Decrypt(key, aad, data []byte) ([]byte, error) {
	ae, err := chacha20poly1305.NewX(key)
	if err != nil {
		return nil, err
	}
	if len(data) < ae.NonceSize() {
		return nil, errors.New("crypto: ciphertext too short")
	}
	nonce, ct := data[:ae.NonceSize()], data[ae.NonceSize():]
	return ae.Open(nil, nonce, ct, aad)
}
