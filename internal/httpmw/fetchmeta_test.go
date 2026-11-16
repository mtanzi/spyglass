package httpmw

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// fetchHeaders are the Sec-Fetch-* values of one request; an empty field is
// not sent.
type fetchHeaders struct{ site, mode, dest string }

// Browser request shapes, as a browser sends them.
var (
	crossSiteFetch      = fetchHeaders{"cross-site", "cors", "empty"}
	crossSiteNoCORS     = fetchHeaders{"cross-site", "no-cors", "script"}
	crossSiteNavigation = fetchHeaders{"cross-site", "navigate", "document"}
	crossSiteIframe     = fetchHeaders{"cross-site", "navigate", "iframe"}
	sameOriginFetch     = fetchHeaders{"same-origin", "cors", "empty"}
	sameOriginAsset     = fetchHeaders{"same-origin", "no-cors", "script"}
	sameSiteFetch       = fetchHeaders{"same-site", "cors", "empty"}
	typedURL            = fetchHeaders{"none", "navigate", "document"}
	noHeaders           = fetchHeaders{}
)

func fetchMetadataStatus(t *testing.T, method, path string, f fetchHeaders) *httptest.ResponseRecorder {
	t.Helper()
	called := false
	h := FetchMetadata(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	r := httptest.NewRequest(method, path, nil)
	for k, v := range map[string]string{"Sec-Fetch-Site": f.site, "Sec-Fetch-Mode": f.mode, "Sec-Fetch-Dest": f.dest} {
		if v != "" {
			r.Header.Set(k, v)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if called != (rec.Code == http.StatusOK) {
		t.Fatalf("%s %s %+v: status %d but next called = %v", method, path, f, rec.Code, called)
	}
	return rec
}

func TestFetchMetadataRefusesCrossSite(t *testing.T) {
	cases := []struct {
		method, path string
		f            fetchHeaders
	}{
		{"GET", "/admin/calls", crossSiteFetch},
		{"GET", "/admin/openapi.json", crossSiteNoCORS},
		{"POST", "/admin/calls", crossSiteFetch},
		{"GET", "/admin/stream", crossSiteFetch},
		{"GET", "/ui/assets/index.js", crossSiteNoCORS},
		{"GET", "/ui/", crossSiteFetch},
		{"GET", "/ui/", crossSiteIframe},
		{"POST", "/ui/", crossSiteNavigation},
		{"GET", "/ui", crossSiteNavigation},
		{"GET", "/admin/calls", crossSiteNavigation},
		{"GET", "/metrics", crossSiteNoCORS},
		{"GET", "/healthz", crossSiteFetch},
		{"GET", "/readyz", crossSiteFetch},
		{"GET", "/ui/", fetchHeaders{site: "cross-site"}},
	}
	for _, c := range cases {
		if rec := fetchMetadataStatus(t, c.method, c.path, c.f); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s %+v = %d, want 403", c.method, c.path, c.f, rec.Code)
		}
	}
}

func TestFetchMetadataAllows(t *testing.T) {
	cases := []struct {
		method, path string
		f            fetchHeaders
	}{
		{"GET", "/ui/", crossSiteNavigation},
		{"GET", "/ui/calls/abc", crossSiteNavigation},
		{"GET", "/ui/", typedURL},
		{"GET", "/ui/assets/index.js", sameOriginAsset},
		{"GET", "/admin/calls", sameOriginFetch},
		{"GET", "/admin/stream", sameOriginFetch},
		{"POST", "/admin/calls", sameOriginFetch},
		{"GET", "/admin/calls", sameSiteFetch},
		{"GET", "/admin/calls", noHeaders},
		{"POST", "/admin/calls", noHeaders},
		{"GET", "/metrics", noHeaders},
		{"GET", "/healthz", noHeaders},
		{"GET", "/readyz", noHeaders},
		{"GET", "/ui/", noHeaders},
		{"GET", "/admin/calls", fetchHeaders{mode: "cors", dest: "empty"}},
	}
	for _, c := range cases {
		if rec := fetchMetadataStatus(t, c.method, c.path, c.f); rec.Code != http.StatusOK {
			t.Errorf("%s %s %+v = %d, want 200", c.method, c.path, c.f, rec.Code)
		}
	}
}

func TestFetchMetadataVaryOnEveryResponse(t *testing.T) {
	for _, f := range []fetchHeaders{crossSiteFetch, crossSiteNavigation, sameOriginFetch, noHeaders} {
		for _, path := range []string{"/ui/", "/admin/calls"} {
			rec := fetchMetadataStatus(t, "GET", path, f)
			if got := rec.Header().Values("Vary"); len(got) != 1 || got[0] != FetchMetadataVary {
				t.Errorf("GET %s %+v: Vary = %q, want %q", path, f, got, FetchMetadataVary)
			}
		}
	}
}

func TestFetchMetadataKeepsExistingVary(t *testing.T) {
	h := FetchMetadata(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/admin/calls", nil))
	want := []string{FetchMetadataVary, "Accept-Encoding"}
	got := rec.Header().Values("Vary")
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Vary = %q, want %q", got, want)
	}
}
