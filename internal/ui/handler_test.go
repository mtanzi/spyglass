package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

const testIndex = "<!doctype html><title>test</title>"

var testFS = fstest.MapFS{
	"index.html":                {Data: []byte(testIndex)},
	"assets/index-abc123.js":    {Data: []byte("console.log(1)")},
	"favicon.svg":               {Data: []byte("<svg/>")},
	"assets/nested/deep-1f.css": {Data: []byte("body{}")},
}

// serve runs the full mounted chain: Mount strips /ui/ and adds the CSP.
func serve(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(Prefix, WithCSP(http.StripPrefix(Prefix, h)))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestIndexAndSPAFallback(t *testing.T) {
	h := newHandler(testFS)
	for _, target := range []string{
		"/ui/",
		"/ui/index.html",
		"/ui/calls",             // deep link
		"/ui/sessions/abc/def",  // deep link, nested; reload serves the same
		"/ui/assets",            // the directory itself is not a file
		"/ui/favicon.svg/extra", // under a file, not a file
		"/ui/%2e%2e/%2e%2e/etc/passwd",
	} {
		rec := serve(t, h, http.MethodGet, target)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status %d, want 200", target, rec.Code)
			continue
		}
		if rec.Body.String() != testIndex {
			t.Errorf("GET %s: body %q, want index.html", target, rec.Body.String())
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("GET %s: Cache-Control %q, want no-cache", target, got)
		}
		if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
			t.Errorf("GET %s: Content-Type %q, want text/html", target, got)
		}
	}
}

func TestAssetCaching(t *testing.T) {
	h := newHandler(testFS)
	for _, target := range []string{"/ui/assets/index-abc123.js", "/ui/assets/nested/deep-1f.css"} {
		rec := serve(t, h, http.MethodGet, target)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status %d", target, rec.Code)
		}
		want := "public, max-age=31536000, immutable"
		if got := rec.Header().Get("Cache-Control"); got != want {
			t.Errorf("GET %s: Cache-Control %q, want %q", target, got, want)
		}
	}
	rec := serve(t, h, http.MethodGet, "/ui/assets/index-abc123.js")
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "javascript") {
		t.Errorf("js Content-Type %q", got)
	}
}

func TestMissingAssetIs404(t *testing.T) {
	rec := serve(t, newHandler(testFS), http.MethodGet, "/ui/assets/gone-deadbeef.js")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "<!doctype") {
		t.Error("a missing asset must not return index.html")
	}
	if got := rec.Header().Get("Cache-Control"); strings.Contains(got, "immutable") {
		t.Errorf("404 Cache-Control %q must not be immutable", got)
	}
}

func TestOtherFileIsNoCache(t *testing.T) {
	rec := serve(t, newHandler(testFS), http.MethodGet, "/ui/favicon.svg")
	if rec.Code != http.StatusOK || rec.Body.String() != "<svg/>" {
		t.Fatalf("status %d body %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control %q, want no-cache", got)
	}
}

// wantCSP is the UI policy of ADR 0058, written out so a change to csp fails.
const wantCSP = "default-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'; require-trusted-types-for 'script'"

func TestCSPOnEveryResponse(t *testing.T) {
	h := newHandler(testFS)
	for _, target := range []string{"/ui/", "/ui/calls", "/ui/assets/index-abc123.js", "/ui/assets/gone.js"} {
		rec := serve(t, h, http.MethodGet, target)
		if got := rec.Header().Get("Content-Security-Policy"); got != wantCSP {
			t.Errorf("GET %s: CSP %q", target, got)
		}
	}
}

func TestMethods(t *testing.T) {
	h := newHandler(testFS)
	if rec := serve(t, h, http.MethodHead, "/ui/calls"); rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Errorf("HEAD: status %d, body %d bytes", rec.Code, rec.Body.Len())
	}
	rec := serve(t, h, http.MethodPost, "/ui/calls")
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") == "" {
		t.Errorf("POST: status %d, Allow %q", rec.Code, rec.Header().Get("Allow"))
	}
}

func TestUnbuiltUIIs404(t *testing.T) {
	rec := serve(t, newHandler(fstest.MapFS{}), http.MethodGet, "/ui/")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", rec.Code)
	}
}

func TestMountRedirectsBarePrefix(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui", nil))
	if rec.Code != http.StatusTemporaryRedirect || rec.Header().Get("Location") != "/ui/" {
		t.Errorf("status %d, Location %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestMountedHandlerServesAndSetsCSP(t *testing.T) {
	mux := http.NewServeMux()
	Mount(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ui/some/deep/link", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if rec.Header().Get("Content-Security-Policy") != wantCSP {
		t.Errorf("CSP %q", rec.Header().Get("Content-Security-Policy"))
	}
	if !strings.Contains(rec.Body.String(), "<html") {
		t.Errorf("body %q is not an HTML page", rec.Body.String())
	}
}
