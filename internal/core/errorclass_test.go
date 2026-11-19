package core

import (
	"net/http"
	"testing"
)

func TestClassifyOutcome(t *testing.T) {
	tests := []struct {
		name   string
		status int
		kind   FailureKind
		want   ErrorClass
	}{
		{"success", 200, FailureNone, ""},
		{"redirect", 304, FailureNone, ""},
		{"bad request", 400, FailureNone, ErrorClassInvalidRequest},
		{"unauthorized", 401, FailureNone, ErrorClassAuth},
		{"forbidden", 403, FailureNone, ErrorClassAuth},
		{"not found", 404, FailureNone, ErrorClassInvalidRequest},
		{"request timeout", 408, FailureNone, ErrorClassTimeout},
		{"too large", 413, FailureNone, ErrorClassInvalidRequest},
		{"rate limited", 429, FailureNone, ErrorClassRateLimited},
		{"upstream 500", 500, FailureNone, ErrorClassUpstream5xx},
		{"bad gateway", 502, FailureNone, ErrorClassUpstream5xx},
		{"gateway timeout", 504, FailureNone, ErrorClassTimeout},
		{"overloaded", 529, FailureNone, ErrorClassUpstream5xx},
		{"client abort", 499, FailureClientAbort, ErrorClassClientAbort},
		{"client abort mid-stream keeps 200", 200, FailureClientAbort, ErrorClassClientAbort},
		{"idle timeout mid-stream keeps 200", 200, FailureIdleTimeout, ErrorClassTimeout},
		{"upstream timeout", 504, FailureUpstreamTimeout, ErrorClassTimeout},
		{"upstream unreachable", 502, FailureUpstreamUnreachable, ErrorClassUpstream5xx},
		{"proxy auth failure", 401, FailureAuth, ErrorClassAuth},
		{"proxy oversize body", 413, FailureInvalidRequest, ErrorClassInvalidRequest},
		{"proxy malformed request", 400, FailureInvalidRequest, ErrorClassInvalidRequest},
		{"proxy internal", 500, FailureInternal, ErrorClassInternal},
	}
	valid := map[ErrorClass]bool{"": true}
	for _, c := range ErrorClasses() {
		valid[c] = true
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyOutcome(tt.status, tt.kind, "")
			if got != tt.want {
				t.Errorf("ClassifyOutcome(%d, %d) = %q, want %q", tt.status, tt.kind, got, tt.want)
			}
			if !valid[got] {
				t.Errorf("class %q is outside the closed vocabulary", got)
			}
		})
	}
}

// TestClassifyOutcomeTotal checks that every status and failure kind maps to
// the empty class or to a member of the closed vocabulary, and that every
// class is reachable.
func TestClassifyOutcomeTotal(t *testing.T) {
	valid := map[ErrorClass]bool{}
	for _, c := range ErrorClasses() {
		valid[c] = true
	}
	seen := map[ErrorClass]bool{}
	for kind := FailureNone; kind <= FailureInternal; kind++ {
		for status := 100; status < 600; status++ {
			got := ClassifyOutcome(status, kind, "")
			if got == "" {
				if kind != FailureNone || status >= http.StatusBadRequest {
					t.Fatalf("ClassifyOutcome(%d, %d) is empty for a failure", status, kind)
				}
				continue
			}
			if !valid[got] {
				t.Fatalf("ClassifyOutcome(%d, %d) = %q, outside the vocabulary", status, kind, got)
			}
			seen[got] = true
		}
	}
	for _, c := range ErrorClasses() {
		if !seen[c] {
			t.Errorf("class %q is never produced", c)
		}
	}
	if len(ErrorClasses()) != 7 {
		t.Errorf("vocabulary has %d classes, want 7: adding one is a deliberate change", len(ErrorClasses()))
	}
}

// TestClassifyOutcomePrecedence pins the rule through the one entry point: a
// failure kind decides alone, else a status of 400 or more, else the class of
// an error event inside the stream, else the empty class.
func TestClassifyOutcomePrecedence(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		kind     FailureKind
		inStream ErrorClass
		want     ErrorClass
	}{
		{"failure beats status", 429, FailureClientAbort, "", ErrorClassClientAbort},
		{"failure beats in-stream error", 200, FailureIdleTimeout, ErrorClassUpstream5xx, ErrorClassTimeout},
		{"status beats in-stream error", 429, FailureNone, ErrorClassUpstream5xx, ErrorClassRateLimited},
		{"in-stream error on success status", 200, FailureNone, ErrorClassRateLimited, ErrorClassRateLimited},
		{"nothing applies", 200, FailureNone, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyOutcome(tt.status, tt.kind, tt.inStream); got != tt.want {
				t.Errorf("ClassifyOutcome(%d, %d, %q) = %q, want %q", tt.status, tt.kind, tt.inStream, got, tt.want)
			}
		})
	}
}
