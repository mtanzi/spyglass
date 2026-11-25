package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
)

// KeyPrefix starts every virtual key the keygen command issues.
const KeyPrefix = "lp_"

// Key is a virtual key as configured: a name and the lowercase hex SHA-256 of
// the plaintext. The plaintext is never held.
type Key struct {
	Name     string
	SHA256   string
	Disabled bool
	// Project names the project every call of this key belongs to (ADR 0059).
	// It ranks above a project the extractor detects. Empty means none.
	Project string
}

var keyHashRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// HashKey returns the stored form of a plaintext virtual key: the lowercase hex
// SHA-256 of the key as presented.
func HashKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// ValidateKeys checks the key rules: a name is required, names are unique, an
// optional project is a valid project name, the hash is 64 lowercase hex
// characters and hashes are unique. It returns one
// error per problem, each prefixed keys[i], and never includes a hash.
func ValidateKeys(keys []Key) []error {
	var errs []error
	add := func(i int, format string, args ...any) {
		errs = append(errs, fmt.Errorf("keys[%d]: %s", i, fmt.Sprintf(format, args...)))
	}
	names := map[string]int{}
	hashes := map[string]int{}
	for i, k := range keys {
		if k.Name == "" {
			add(i, "name is required")
		} else if j, dup := names[k.Name]; dup {
			add(i, "duplicate name %q (also keys[%d])", k.Name, j)
		} else {
			names[k.Name] = i
		}
		if k.Project != "" {
			if p, ok := ProjectName(k.Project); !ok || p != k.Project {
				add(i, "project must be 1 to %d printable characters with no space at either end", MaxProjectLen)
			}
		}
		if !keyHashRE.MatchString(k.SHA256) {
			add(i, "sha256 must be 64 lowercase hex characters")
		} else if j, dup := hashes[k.SHA256]; dup {
			add(i, "duplicate sha256 (also keys[%d])", j)
		} else {
			hashes[k.SHA256] = i
		}
	}
	return errs
}
