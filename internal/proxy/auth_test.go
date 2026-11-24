package proxy

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mtanzi/spyglass/internal/config"
	"github.com/mtanzi/spyglass/internal/core"
)

type fakeKeys map[string]core.Key

func (f fakeKeys) Lookup(h string) (core.Key, bool) { k, ok := f[h]; return k, ok }

func TestAuthenticate(t *testing.T) {
	keys := fakeKeys{
		core.HashKey("lp_good"): {Name: "good"},
		core.HashKey("lp_off"):  {Name: "off", Disabled: true},
	}
	hdr := func(kv ...string) http.Header {
		h := http.Header{}
		for i := 0; i < len(kv); i += 2 {
			h.Set(kv[i], kv[i+1])
		}
		return h
	}
	cases := []struct {
		name     string
		h        http.Header
		wantName string
		wantMsg  string // empty means success
	}{
		{"bearer", hdr("Authorization", "Bearer lp_good"), "good", ""},
		{"bearer lowercase scheme", hdr("Authorization", "bearer lp_good"), "good", ""},
		{"x-api-key", hdr("X-Api-Key", "lp_good"), "good", ""},
		{"bearer wins over x-api-key", hdr("Authorization", "Bearer lp_good", "X-Api-Key", "lp_nope"), "good", ""},
		{"non-bearer authorization falls back to x-api-key", hdr("Authorization", "Basic abc", "X-Api-Key", "lp_good"), "good", ""},
		{"unknown", hdr("Authorization", "Bearer lp_nope"), "", "invalid x-api-key"},
		{"disabled", hdr("X-Api-Key", "lp_off"), "", "this API key is disabled"},
		{"missing", hdr(), "", "invalid x-api-key"},
		{"empty bearer", hdr("Authorization", "Bearer "), "", "invalid x-api-key"},
		{"non-bearer only", hdr("Authorization", "Basic abc"), "", "invalid x-api-key"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			key, resp, ok := Authenticate(c.h, keys, config.AuthProxyKey, "req_1")
			if c.wantMsg == "" {
				if !ok || key.Name != c.wantName {
					t.Fatalf("got %q ok=%v; want %q", key.Name, ok, c.wantName)
				}
				return
			}
			if ok || key.Name != "" {
				t.Fatalf("got %q ok=%v; want failure", key.Name, ok)
			}
			if resp.Status != http.StatusUnauthorized {
				t.Errorf("status = %d", resp.Status)
			}
			if !strings.Contains(string(resp.Body), c.wantMsg) {
				t.Errorf("body %s lacks %q", resp.Body, c.wantMsg)
			}
			if resp.Header.Get("X-Request-Id") != "req_1" {
				t.Errorf("request id header = %q", resp.Header.Get("X-Request-Id"))
			}
			if strings.Contains(string(resp.Body), "lp_") {
				t.Errorf("body echoes a key: %s", resp.Body)
			}
		})
	}
}

func TestAuthenticateModes(t *testing.T) {
	keys := fakeKeys{
		core.HashKey("lp_good"): {Name: "good"},
		core.HashKey("lp_off"):  {Name: "off", Disabled: true},
	}
	cases := []struct {
		name     string
		mode     string
		kv       []string
		wantName string
		wantMsg  string // empty means success
	}{
		{"passthrough key header", config.AuthClientPassthrough, []string{"X-Spyglass-Key", "lp_good", "Authorization", "Bearer sk-ant-oat-client"}, "good", ""},
		{"passthrough key header with client x-api-key", config.AuthClientPassthrough, []string{"X-Spyglass-Key", "lp_good", "X-Api-Key", "sk-ant-api03-client"}, "good", ""},
		{"passthrough no key header", config.AuthClientPassthrough, []string{"Authorization", "Bearer sk-ant-oat-client"}, "", "invalid x-api-key"},
		{"passthrough disabled key", config.AuthClientPassthrough, []string{"X-Spyglass-Key", "lp_off"}, "", "this API key is disabled"},
		{"passthrough lp_ in Authorization", config.AuthClientPassthrough, []string{"X-Spyglass-Key", "lp_good", "Authorization", "Bearer lp_good"}, "", "invalid x-api-key"},
		{"passthrough lp_ in x-api-key", config.AuthClientPassthrough, []string{"X-Spyglass-Key", "lp_good", "X-Api-Key", "lp_other"}, "", "invalid x-api-key"},
		{"proxy_key with key header", config.AuthProxyKey, []string{"Authorization", "Bearer lp_good", "X-Spyglass-Key", "lp_good"}, "", "invalid x-api-key"},
		{"proxy_key with empty key header", config.AuthProxyKey, []string{"Authorization", "Bearer lp_good", "X-Spyglass-Key", ""}, "", "invalid x-api-key"},
		{"proxy_key bearer", config.AuthProxyKey, []string{"Authorization", "Bearer lp_good"}, "good", ""},
		// The x-llm-proxy-key of 0.1.0 is no longer read (ADR 0054): a request
		// that carries only it carries no virtual key.
		{"passthrough legacy key header only", config.AuthClientPassthrough, []string{"X-Llm-Proxy-Key", "lp_good", "Authorization", "Bearer sk-ant-oat-client"}, "", "invalid x-api-key"},
		{"passthrough legacy key header alone", config.AuthClientPassthrough, []string{"X-Llm-Proxy-Key", "lp_good"}, "", "invalid x-api-key"},
		{"passthrough legacy lp_ in Authorization", config.AuthClientPassthrough, []string{"X-Llm-Proxy-Key", "lp_good", "Authorization", "Bearer lp_good"}, "", "invalid x-api-key"},
		{"passthrough both headers, legacy ignored", config.AuthClientPassthrough, []string{"X-Spyglass-Key", "lp_good", "X-Llm-Proxy-Key", "lp_nope"}, "good", ""},
		{"passthrough both headers, new unknown key is not rescued", config.AuthClientPassthrough, []string{"X-Spyglass-Key", "lp_nope", "X-Llm-Proxy-Key", "lp_good"}, "", "invalid x-api-key"},
		{"proxy_key ignores legacy key header", config.AuthProxyKey, []string{"Authorization", "Bearer lp_good", "X-Llm-Proxy-Key", "lp_good"}, "good", ""},
		{"proxy_key legacy key header is no credential", config.AuthProxyKey, []string{"X-Llm-Proxy-Key", "lp_good"}, "", "invalid x-api-key"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := http.Header{}
			for i := 0; i < len(c.kv); i += 2 {
				h.Set(c.kv[i], c.kv[i+1])
			}
			key, resp, ok := Authenticate(h, keys, c.mode, "req_1")
			if c.wantMsg == "" {
				if !ok || key.Name != c.wantName {
					t.Fatalf("got %q ok=%v; want %q", key.Name, ok, c.wantName)
				}
				return
			}
			if ok || resp.Status != http.StatusUnauthorized || !strings.Contains(string(resp.Body), c.wantMsg) {
				t.Fatalf("ok=%v status=%d body=%s; want 401 %q", ok, resp.Status, resp.Body, c.wantMsg)
			}
		})
	}
}
