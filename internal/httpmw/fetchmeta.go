package httpmw

import (
	"net/http"
	"strings"
)

// FetchMetadataVary is the Vary value FetchMetadata adds to every response: the
// answer depends on these request headers, so a cache must not reuse it across
// them.
const FetchMetadataVary = "Sec-Fetch-Site, Sec-Fetch-Mode, Sec-Fetch-Dest"

// uiPrefix is the path the web UI is mounted at, ui.Prefix. It is repeated
// here so httpmw does not import the embedded UI.
const uiPrefix = "/ui/"

// FetchMetadata is the admin plane's Fetch Metadata isolation (ADR 0058). It
// answers 403 to a request whose Sec-Fetch-Site is cross-site, unless the
// request is a top-level GET navigation to the UI: Sec-Fetch-Mode navigate,
// Sec-Fetch-Dest document and a path under /ui/. A same-origin, same-site or
// user-initiated (none) request passes, and so does a request without
// Sec-Fetch-Site: curl, Prometheus and the CLI send none. So another web page
// cannot read from or post to the loopback admin plane, which has no sign-in,
// while a link to the UI still opens it. Every response gets the Vary header
// FetchMetadataVary. The data plane does not use it.
func FetchMetadata(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", FetchMetadataVary)
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" && !uiNavigation(r) {
			http.Error(w, "cross-site request refused", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// uiNavigation reports whether r is a top-level GET navigation to the UI.
func uiNavigation(r *http.Request) bool {
	return r.Method == http.MethodGet &&
		r.Header.Get("Sec-Fetch-Mode") == "navigate" &&
		r.Header.Get("Sec-Fetch-Dest") == "document" &&
		strings.HasPrefix(r.URL.Path, uiPrefix)
}
