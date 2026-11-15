package httpmw

import "net/http"

// PermissionsPolicy turns off the powerful browser features the admin UI never
// uses: camera, microphone, geolocation and similar device, sensor and
// payment features (ADR 0058). An empty allowlist, (), denies a feature to the
// page and to every frame.
const PermissionsPolicy = "accelerometer=(), camera=(), display-capture=(), geolocation=(), " +
	"gyroscope=(), hid=(), magnetometer=(), microphone=(), midi=(), payment=(), " +
	"publickey-credentials-get=(), screen-wake-lock=(), serial=(), usb=(), xr-spatial-tracking=()"

// securityHeaders are the headers SecurityHeaders sets, by canonical name.
var securityHeaders = [...][2]string{
	{"X-Content-Type-Options", "nosniff"},
	{"Referrer-Policy", "no-referrer"},
	{"Cross-Origin-Resource-Policy", "same-origin"},
	{"Cross-Origin-Opener-Policy", "same-origin"},
	{"X-Frame-Options", "DENY"},
	{"Permissions-Policy", PermissionsPolicy},
}

// SecurityHeaders sets the browser security headers of the admin plane on
// every response of next (ADR 0058): no MIME sniffing, no referrer, no
// cross-origin reads or openers, no framing and no powerful features. The
// headers only change what a browser does, so /metrics, /healthz, /readyz and
// clients without a browser see the same responses as before. It runs outside
// Wrap, so a recovered panic carries the headers too. The data plane does not
// use it: its responses pass the upstream headers through unchanged.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		for _, kv := range securityHeaders {
			h.Set(kv[0], kv[1])
		}
		next.ServeHTTP(w, r)
	})
}
