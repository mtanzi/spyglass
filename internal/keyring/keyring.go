package keyring

import (
	"errors"
	"fmt"

	"github.com/mtanzi/spyglass/internal/core"
)

// Keyring is an immutable map from SHA-256 to key. It is safe for concurrent
// use because nothing modifies it after New.
type Keyring struct {
	byHash map[string]core.Key
}

// New builds a Keyring from keys. It rejects an empty name, a duplicate name,
// a hash that is not 64 lowercase hex characters and a duplicate hash. The
// error lists every problem and never contains a key hash.
func New(keys []core.Key) (*Keyring, error) {
	if errs := core.ValidateKeys(keys); len(errs) > 0 {
		return nil, fmt.Errorf("invalid keys: %w", errors.Join(errs...))
	}
	byHash := make(map[string]core.Key, len(keys))
	for _, k := range keys {
		byHash[k.SHA256] = k
	}
	return &Keyring{byHash: byHash}, nil
}

// Lookup returns the key with the given lowercase hex SHA-256.
func (r *Keyring) Lookup(sha256 string) (core.Key, bool) {
	k, ok := r.byHash[sha256]
	return k, ok
}

// Len returns the number of keys.
func (r *Keyring) Len() int { return len(r.byHash) }
