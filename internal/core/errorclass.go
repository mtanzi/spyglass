package core

import "net/http"

// ErrorClass is the closed vocabulary of what went wrong with a call. It is
// stored as CallRecord.ErrorClass; an empty string means the call succeeded.
// A new class is a deliberate change: add it here and to the mapping test.
type ErrorClass string

const (
	ErrorClassRateLimited    ErrorClass = "rate_limited"
	ErrorClassUpstream5xx    ErrorClass = "upstream_5xx"
	ErrorClassTimeout        ErrorClass = "timeout"
	ErrorClassAuth           ErrorClass = "auth"
	ErrorClassClientAbort    ErrorClass = "client_abort"
	ErrorClassInvalidRequest ErrorClass = "invalid_request"
	ErrorClassInternal       ErrorClass = "internal"
)

// ErrorClasses lists every class, in the order the documents name them.
func ErrorClasses() []ErrorClass {
	return []ErrorClass{
		ErrorClassRateLimited, ErrorClassUpstream5xx, ErrorClassTimeout,
		ErrorClassAuth, ErrorClassClientAbort, ErrorClassInvalidRequest,
		ErrorClassInternal,
	}
}

// FailureKind says how a call failed when the HTTP status alone does not tell.
// It is the proxy's own observation, so it takes precedence over the status.
type FailureKind int

const (
	// FailureNone means the proxy saw no failure of its own; the status decides.
	FailureNone FailureKind = iota
	// FailureClientAbort: the client went away before the call finished.
	FailureClientAbort
	// FailureIdleTimeout: the idle-between-chunks watchdog fired on a stream.
	FailureIdleTimeout
	// FailureUpstreamTimeout: dial or time-to-headers timeout towards the upstream.
	FailureUpstreamTimeout
	// FailureUpstreamUnreachable: the upstream could not be reached (502).
	FailureUpstreamUnreachable
	// FailureAuth: the proxy rejected the virtual key.
	FailureAuth
	// FailureInvalidRequest: the proxy rejected the request (malformed, oversize).
	FailureInvalidRequest
	// FailureInternal: a bug or panic inside the proxy.
	FailureInternal
)

// ClassifyOutcome is the one place that decides the error class of a call.
// A FailureKind other than FailureNone decides alone. Otherwise the HTTP
// status decides, and a status of 400 or more always gives a class. Only a
// status below 400 falls back to inStream, the class of an error event the
// provider sent inside a successful response; the caller passes the empty
// class when there is none. An outcome with none of these is a success and
// gives the empty class. Every outcome gets at most one class.
func ClassifyOutcome(status int, kind FailureKind, inStream ErrorClass) ErrorClass {
	switch kind {
	case FailureClientAbort:
		return ErrorClassClientAbort
	case FailureIdleTimeout, FailureUpstreamTimeout:
		return ErrorClassTimeout
	case FailureUpstreamUnreachable:
		return ErrorClassUpstream5xx
	case FailureAuth:
		return ErrorClassAuth
	case FailureInvalidRequest:
		return ErrorClassInvalidRequest
	case FailureInternal:
		return ErrorClassInternal
	}
	switch {
	case status < 400:
		return inStream
	case status == http.StatusUnauthorized, status == http.StatusForbidden:
		return ErrorClassAuth
	case status == http.StatusTooManyRequests:
		return ErrorClassRateLimited
	case status == http.StatusRequestTimeout, status == http.StatusGatewayTimeout:
		return ErrorClassTimeout
	case status >= 500:
		return ErrorClassUpstream5xx
	default:
		return ErrorClassInvalidRequest
	}
}
