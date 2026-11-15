package httpmw

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// wantSecurityHeaders is the admin-plane header set of ADR 0058, written out
// so a change to the middleware fails.
var wantSecurityHeaders = map[string]string{
	"X-Content-Type-Options":       "nosniff",
	"Referrer-Policy":              "no-referrer",
	"Cross-Origin-Resource-Policy": "same-origin",
	"Cross-Origin-Opener-Policy":   "same-origin",
	"X-Frame-Options":              "DENY",
	"Permissions-Policy":           PermissionsPolicy,
}

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	handlers := map[string]http.Handler{
		"ok":        http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }),
		"not found": http.NotFoundHandler(),
		"panic":     Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }), Options{Logger: discard()}),
	}
	for name, next := range handlers {
		rec := httptest.NewRecorder()
		SecurityHeaders(next).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		for k, want := range wantSecurityHeaders {
			if got := rec.Header().Values(k); len(got) != 1 || got[0] != want {
				t.Errorf("%s: %s = %q, want %q", name, k, got, want)
			}
		}
	}
}

func TestPermissionsPolicyTurnsOffDeviceFeatures(t *testing.T) {
	directives := map[string]bool{}
	for _, d := range strings.Split(PermissionsPolicy, ",") {
		directives[strings.TrimSpace(d)] = true
	}
	for _, d := range []string{"camera=()", "microphone=()", "geolocation=()"} {
		if !directives[d] {
			t.Errorf("Permissions-Policy %q lacks %s", PermissionsPolicy, d)
		}
	}
}
