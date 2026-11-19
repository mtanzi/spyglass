package main

import (
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"strings"
	"unicode"

	"github.com/mtanzi/spyglass/internal/core"
	"go.yaml.in/yaml/v3"
)

const (
	keyBytes    = 32
	base62      = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	base62Width = 43 // 62^43 > 2^256, so 32 bytes always fit
)

// runKeygen prints a new key and its keys entry. It reads no config and opens
// no database. The plaintext goes to stdout only.
func runKeygen(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("name", "", "key name for the keys entry (required)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "spyglass keygen: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	if err := validateKeyName(*name); err != nil {
		_, _ = fmt.Fprintf(stderr, "spyglass keygen: %v\n", err)
		return 2
	}
	key, err := newKey(rand.Reader)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "spyglass keygen: %v\n", err)
		return 1
	}
	out, err := formatKeygen(*name, key)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "spyglass keygen: %v\n", err)
		return 1
	}
	_, _ = io.WriteString(stdout, out)
	return 0
}

// validateKeyName rejects an empty name and a name with whitespace. A name may
// contain ':' (for example fm:<task-id>).
func validateKeyName(name string) error {
	if name == "" {
		return errors.New("--name is required")
	}
	if strings.IndexFunc(name, unicode.IsSpace) >= 0 {
		return fmt.Errorf("--name %q must not contain whitespace", name)
	}
	return nil
}

// newKey draws 32 bytes from r and returns lp_ plus their base62 encoding,
// zero-padded to a fixed width.
func newKey(r io.Reader) (string, error) {
	var b [keyBytes]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	n := new(big.Int).SetBytes(b[:])
	base := big.NewInt(62)
	mod := new(big.Int)
	enc := make([]byte, base62Width)
	for i := base62Width - 1; i >= 0; i-- {
		n.DivMod(n, base, mod)
		enc[i] = base62[mod.Int64()]
	}
	return core.KeyPrefix + string(enc), nil
}

type keyEntry struct {
	Name   string `yaml:"name"`
	SHA256 string `yaml:"sha256"`
}

// formatKeygen renders the plaintext on one marked line, then the keys entry
// as YAML to paste under keys:.
func formatKeygen(name, key string) (string, error) {
	entry, err := yaml.Marshal([]keyEntry{{Name: name, SHA256: core.HashKey(key)}})
	if err != nil {
		return "", fmt.Errorf("render keys entry: %w", err)
	}
	return fmt.Sprintf("plaintext key (shown once, give it to the client and keep it nowhere else): %s\n\nkeys entry (add under keys: in the config):\n%s", key, entry), nil
}
