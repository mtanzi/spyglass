package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/mtanzi/spyglass/internal/config"
	"github.com/mtanzi/spyglass/internal/core"
	"github.com/mtanzi/spyglass/internal/family/anthropic"
)

// KeyLookup finds a virtual key by the lowercase hex SHA-256 of its plaintext.
// keyring.Keyring implements it.
type KeyLookup interface {
	Lookup(sha256 string) (core.Key, bool)
}

// virtualKeyPrefix starts every virtual key the keygen command issues.
const virtualKeyPrefix = "lp_"

// Authenticate checks the virtual key a request presents. Where the key is
// read from depends on the provider's auth_mode: in proxy_key mode it is
// Authorization: Bearer <key>, otherwise x-api-key; in client_passthrough mode
// the client's own credential occupies those headers, so the key is read from
// x-spyglass-key. The x-llm-proxy-key of release 0.1.0 is no longer read: a
// request that carries only that header carries no virtual key (ADR 0054). A
// credential in the wrong place for the mode is rejected before any lookup, so
// a virtual key cannot reach the upstream by accident: x-spyglass-key on a
// proxy_key provider, or an lp_ value in Authorization or x-api-key on a
// client_passthrough provider.
//
// It returns the key name, which the correlation step stores as key_name. On
// failure it returns a protocol-correct 401 and ok false: a missing, unknown or
// misplaced key is MissingOrInvalidKeyError, a disabled key is
// DisabledKeyError. The presented key is hashed at once and never logged,
// stored or returned. The client's own credential in passthrough mode is only
// inspected for the lp_ prefix; it is never hashed, kept or logged.
func Authenticate(h http.Header, keys KeyLookup, mode, requestID string) (name string, errResp anthropic.ErrorResponse, ok bool) {
	var presented string
	switch mode {
	case config.AuthClientPassthrough:
		if misplacedKey(h) {
			return "", anthropic.MissingOrInvalidKeyError(requestID), false
		}
		presented = strings.TrimSpace(h.Get(headerProxyKey))
	default:
		if len(h.Values(headerProxyKey)) > 0 {
			return "", anthropic.MissingOrInvalidKeyError(requestID), false
		}
		presented = presentedKey(h)
	}
	if presented == "" {
		return "", anthropic.MissingOrInvalidKeyError(requestID), false
	}
	sum := sha256.Sum256([]byte(presented))
	key, found := keys.Lookup(hex.EncodeToString(sum[:]))
	if !found {
		return "", anthropic.MissingOrInvalidKeyError(requestID), false
	}
	if key.Disabled {
		return "", anthropic.DisabledKeyError(requestID), false
	}
	return key.Name, anthropic.ErrorResponse{}, true
}

// misplacedKey reports whether Authorization or x-api-key carries a virtual
// key, which a client_passthrough provider would forward upstream.
func misplacedKey(h http.Header) bool {
	for _, v := range h.Values("Authorization") {
		v = strings.TrimSpace(v)
		if scheme, token, found := strings.Cut(v, " "); found && strings.EqualFold(scheme, "Bearer") {
			v = strings.TrimSpace(token)
		}
		if strings.HasPrefix(v, virtualKeyPrefix) {
			return true
		}
	}
	for _, v := range h.Values("X-Api-Key") {
		if strings.HasPrefix(strings.TrimSpace(v), virtualKeyPrefix) {
			return true
		}
	}
	return false
}

// presentedKey returns the credential from the Authorization bearer form, or
// else from x-api-key. It returns "" when neither carries one.
func presentedKey(h http.Header) string {
	if scheme, token, found := strings.Cut(strings.TrimSpace(h.Get("Authorization")), " "); found &&
		strings.EqualFold(scheme, "Bearer") {
		if token = strings.TrimSpace(token); token != "" {
			return token
		}
	}
	return strings.TrimSpace(h.Get("X-Api-Key"))
}
