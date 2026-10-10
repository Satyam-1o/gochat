// Package identity manages the persistent libp2p identity keypair.
// Keeping the key on disk means the peer ID is stable across restarts.
package identity

import (
	"crypto/rand"
	"fmt"
	"os"

	"github.com/libp2p/go-libp2p/core/crypto"
)

// LoadOrCreate loads a private key from path, or generates and persists a new
// Ed25519 key if none exists yet. The file is written with 0600 permissions.
func LoadOrCreate(path string) (crypto.PrivKey, error) {
	if data, err := os.ReadFile(path); err == nil {
		if len(data) == 0 {
			return nil, fmt.Errorf("identity: %s is empty", path)
		}
		priv, err := crypto.UnmarshalPrivateKey(data)
		if err != nil {
			return nil, fmt.Errorf("identity: parse key at %s: %w", path, err)
		}
		return priv, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("identity: read key at %s: %w", path, err)
	}

	priv, _, err := crypto.GenerateKeyPairWithReader(crypto.Ed25519, -1, rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("identity: generate key: %w", err)
	}
	data, err := crypto.MarshalPrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("identity: encode key: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return nil, fmt.Errorf("identity: persist key: %w", err)
	}
	return priv, nil
}
