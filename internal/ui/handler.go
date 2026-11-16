package ui

import (
	"bytes"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// Prefix is the URL path the UI is mounted at, with the trailing slash.
const Prefix = "/ui/"

const (
	indexName    = "index.html"
	assetsPrefix = "assets/"

	cacheImmutable = "public, max-age=31536000, immutable"
	cacheNoCache   = "no-cache"

	// csp allows only same-origin loads: no inline script, no third-party
	// origin. The build emits no inline style, so default-src covers styles too.
	// It also blocks plugins, a <base> element, framing by any page and form
	// posts to another origin. Trusted Types is enforced with no policy, so any
	// write of a string to an HTML or script sink throws (ADR 0058).
	csp = "default-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'; require-trusted-types-for 'script'"
)

// Mount registers the UI on mux at Prefix, behind the CSP header.
func Mount(mux *http.ServeMux) {
	mux.Handle(Prefix, WithCSP(http.StripPrefix(Prefix, Handler())))
}

// WithCSP sets the Content-Security-Policy header on every response of next.
func WithCSP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", csp)
		next.ServeHTTP(w, r)
	})
}

// newHandler serves the built UI from fsys, whose root holds index.html. It
// expects the request path with Prefix stripped. A file under assets/ is
// immutable, because its name carries a content hash. index.html is no-cache,
// so a new release loads at once. Any other path that is not a file returns
// index.html, so a deep link or a reload reaches the client-side router.
func newHandler(fsys fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" || name == indexName {
			serveIndex(w, r, fsys)
			return
		}
		if strings.HasPrefix(name, assetsPrefix) {
			// A missing asset is a 404, never index.html: a stale hashed
			// name must not load HTML as a script.
			if !isFile(fsys, name) {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Cache-Control", cacheImmutable)
			http.ServeFileFS(w, r, fsys, name)
			return
		}
		if isFile(fsys, name) {
			w.Header().Set("Cache-Control", cacheNoCache)
			http.ServeFileFS(w, r, fsys, name)
			return
		}
		serveIndex(w, r, fsys)
	})
}

func isFile(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && !info.IsDir()
}

// serveIndex writes index.html directly. http.ServeFileFS redirects a request
// for /index.html to ./, which would break the fallback.
func serveIndex(w http.ResponseWriter, r *http.Request, fsys fs.FS) {
	b, err := fs.ReadFile(fsys, indexName)
	if err != nil {
		http.Error(w, "ui not built", http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", cacheNoCache)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, indexName, time.Time{}, bytes.NewReader(b))
}
