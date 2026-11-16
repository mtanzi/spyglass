// Package httpmw holds the request-lifecycle steps both listeners share:
// recover, request id, access log and the request-size limit (ADR 0006). The
// steps are plain functions that wrap an http.Handler; Wrap fixes their order.
// SecurityHeaders is the admin plane's browser security headers and
// FetchMetadata its refusal of cross-site browser requests (ADR 0058).
package httpmw
